package sessioncore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// Manager owns lifecycle + outbound client RPC plumbing for stateful sessions.
// It leverages SessionHost primitives only; no additional cross-node state is
// required beyond the event topics used for rendezvous.
type Manager struct {
	Host sessions.SessionHost

	// config
	ttl         time.Duration
	maxLifetime time.Duration

	// internal request id counter fallback (used only if random id generation fails)
	fallbackCounter atomic.Uint64
}

// New constructs a Manager with the provided host. Options can adjust
// defaults; if ttl is zero a 24h default is applied.
func New(host sessions.SessionHost, opts ...ManagerOption) *Manager {
	m := &Manager{Host: host, ttl: 24 * time.Hour}
	for _, opt := range opts {
		opt(m)
	}
	if m.ttl <= 0 {
		m.ttl = 24 * time.Hour
	}
	return m
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithTTL overrides the sliding TTL used for sessions.
func WithTTL(d time.Duration) ManagerOption { return func(m *Manager) { m.ttl = d } }

// WithMaxLifetime sets an absolute maximum lifetime horizon (0 = disabled).
func WithMaxLifetime(d time.Duration) ManagerOption { return func(m *Manager) { m.maxLifetime = d } }

// CreateSession creates and persists new session metadata and returns a handle.
func (m *Manager) CreateSession(ctx context.Context, userID string, protocolVersion string, caps sessions.CapabilitySet, meta sessions.MetadataClientInfo) (*SessionHandle, error) {
	if m == nil || m.Host == nil {
		return nil, fmt.Errorf("session manager not initialized")
	}
	if userID == "" { // user scoping required for auth boundary
		return nil, fmt.Errorf("user id required")
	}
	sid, err := newSessionID()
	if err != nil { // extremely unlikely; fallback to counter-based id
		n := m.fallbackCounter.Add(1)
		sid = fmt.Sprintf("s-%d", n)
	}
	now := time.Now().UTC()
	metaRec := &sessions.SessionMetadata{
		MetaVersion:     1,
		SessionID:       sid,
		UserID:          userID,
		ProtocolVersion: protocolVersion,
		Client:          meta,
		Capabilities:    caps,
		CreatedAt:       now,
		UpdatedAt:       now,
		LastAccess:      now,
		TTL:             m.ttl,
		MaxLifetime:     m.maxLifetime,
		Revoked:         false,
	}
	if err := m.Host.CreateSession(ctx, metaRec); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return newHandle(m, metaRec), nil
}

// LoadSession loads an existing session (verifying ownership) and returns a handle.
// unknown param retained for forward compatibility (placeholder for token-originated issuer, etc.).
func (m *Manager) LoadSession(ctx context.Context, sessID string, userID string, _ string) (*SessionHandle, error) {
	if m == nil || m.Host == nil {
		return nil, fmt.Errorf("session manager not initialized")
	}
	metaRec, err := m.Host.GetSession(ctx, sessID)
	if err != nil {
		return nil, err
	}
	if metaRec.Revoked || metaRec.UserID == "" || metaRec.UserID != userID {
		// Treat as not found to avoid oracle on existence.
		return nil, errors.New("not found")
	}
	// Best-effort sliding TTL touch.
	_ = m.Host.TouchSession(ctx, sessID)
	return newHandle(m, metaRec), nil
}

// DeleteSession hard-deletes a session (idempotent best-effort).
func (m *Manager) DeleteSession(ctx context.Context, sessID string) error {
	if m == nil || m.Host == nil {
		return fmt.Errorf("session manager not initialized")
	}
	return m.Host.DeleteSession(ctx, sessID)
}

// DeliverResponse publishes a JSON-RPC response (received via transport POST)
// onto the server-internal event bus so any waiting outbound call can resume.
// Safe for concurrent use; silently ignores nil or responses without ID.
func (m *Manager) DeliverResponse(ctx context.Context, sessID string, resp *jsonrpc.Response) error {
	if resp == nil || resp.ID == nil {
		return nil
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	topic := rpcResponseTopic(resp.ID.String())
	return m.Host.PublishEvent(ctx, sessID, topic, b)
}

// --- Outbound client RPC plumbing ---

// clientCall emits a server->client JSON-RPC request and blocks for a response
// or context cancellation. Result JSON is decoded into out if non-nil.
func (m *Manager) clientCall(ctx context.Context, h *SessionHandle, method string, params any, out any) error {
	// Build random request id (unguessable & unique). If entropy fails, fallback to incrementing counter.
	var rid string
	if id, err := randomID(); err == nil {
		rid = id
	} else {
		rid = fmt.Sprintf("r-%d", m.fallbackCounter.Add(1))
	}

	// Pre-subscribe to response topic.
	topic := rpcResponseTopic(rid)
	respCh := make(chan []byte, 1)
	subCtx, cancel := context.WithCancel(ctx)
	unsub, err := m.Host.SubscribeEvents(subCtx, h.id, topic, func(ctx context.Context, payload []byte) error {
		select {
		case respCh <- append([]byte(nil), payload...):
		default:
		}
		return nil
	})
	if err != nil {
		cancel()
		return fmt.Errorf("subscribe response: %w", err)
	}
	defer func() {
		unsub()
		cancel()
	}()

	// Marshal params
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params: %w", err)
		}
		paramsRaw = b
	}
	// Construct request (string id)
	id := jsonrpc.NewRequestID(rid)
	req := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: method, Params: paramsRaw, ID: id}
	rawReq, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	// Opportunistic direct write (stream-first semantics). If a direct writer is
	// installed and succeeds, we skip durable publish (original semantics). If it
	// fails, fall back to durable publish so the client can still recover via GET.
	if ok, derr := h.tryDirectWrite(ctx, rawReq); ok {
		if derr != nil { // fallback to durability on error
			if _, err := m.Host.PublishSession(ctx, h.id, rawReq); err != nil {
				return fmt.Errorf("publish request after direct write failure: %w (direct error: %v)", err, derr)
			}
		}
	} else { // no direct writer
		if _, err := m.Host.PublishSession(ctx, h.id, rawReq); err != nil {
			return fmt.Errorf("publish request: %w", err)
		}
	}

	// Await response or cancellation
	select {
	case <-ctx.Done():
		return ctx.Err()
	case raw := <-respCh:
		var resp jsonrpc.Response
		if err := json.Unmarshal(raw, &resp); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		if resp.Error != nil {
			return fmt.Errorf("client error (%d): %s", resp.Error.Code, resp.Error.Message)
		}
		if out != nil && resp.Result != nil {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("decode result: %w", err)
			}
		}
		return nil
	}
}

// rpcResponseTopic returns the server-internal event topic used to rendezvous a response id.
func rpcResponseTopic(id string) string { return "rpcresp:" + id }

// randomID returns a hex-encoded 16-byte cryptographically random string.
func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// newSessionID returns a cryptographically random session id with a prefix.
func newSessionID() (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	return "sess-" + id, nil
}

// newHandle constructs a SessionHandle from metadata.
func newHandle(m *Manager, meta *sessions.SessionMetadata) *SessionHandle {
	h := &SessionHandle{
		id:              meta.SessionID,
		userID:          meta.UserID,
		protocolVersion: meta.ProtocolVersion,
		caps:            meta.Capabilities,
		host:            m.Host,
		mgr:             m,
	}
	// Lazy capability wrappers are created on-demand; nothing to do here.
	return h
}

// (removed unused helper constants and sanitizeMethod)
