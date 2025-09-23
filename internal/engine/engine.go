package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/internal/logctx"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
)

const defaultSessionTTL = 1 * time.Hour
const defaultSessionMaxLifetime = 24 * time.Hour

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrInvalidUserID   = errors.New("invalid user id")
	ErrInternal        = errors.New("internal error")
)

// Engine is the core of an MCP server, coordinating sessions, message routing,
// and protocol handling. It is protocol-agnostic and can be used with different
// transport layers (e.g., HTTP, stdio) by implementing the necessary
// session host and I/O handlers.
type Engine struct {
	host sessions.SessionHost
	srv  mcpservice.ServerCapabilities
	log  *slog.Logger

	// session config
	sessionTTL         time.Duration
	sessionMaxLifetime time.Duration
}

func NewEngine(host sessions.SessionHost, srv mcpservice.ServerCapabilities, opts ...EngineOption) *Engine {
	e := &Engine{
		host:               host,
		srv:                srv,
		log:                slog.Default(),
		sessionTTL:         defaultSessionTTL,
		sessionMaxLifetime: defaultSessionMaxLifetime,
	}
	return e
}

// EngineOption configures a Engine.
type EngineOption func(*Engine)

// WithSessionTTL overrides the sliding TTL used for sessions.
func WithSessionTTL(d time.Duration) EngineOption { return func(m *Engine) { m.sessionTTL = d } }

// WithSessionMaxLifetime sets an absolute maximum lifetime horizon (0 = disabled).
func WithSessionMaxLifetime(d time.Duration) EngineOption {
	return func(m *Engine) { m.sessionMaxLifetime = d }
}

// WithLogger sets a custom logger for the Engine.
func WithLogger(l *slog.Logger) EngineOption {
	return func(m *Engine) {
		if l != nil {
			m.log = l
		}
	}
}

func (e *Engine) HandleRequest(ctx context.Context, sessID, userID string, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	start := time.Now()
	sess, err := e.loadSession(ctx, sessID, userID)
	if err != nil {
		return nil, err
	}

	log := logctx.Enrich(ctx, e.log.With(
		slog.String("session_id", sess.SessionID()),
		slog.String("user_id", sess.userID),
		slog.String("method", req.Method),
	))

	switch req.Method {
	case string(mcp.LoggingSetLevelMethod):
		var params mcp.SetLevelRequest
		if err := json.Unmarshal(req.Params, &params); err != nil {
			log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", err.Error()), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
		}

		switch params.Level {
		case mcp.LoggingLevelDebug, mcp.LoggingLevelInfo, mcp.LoggingLevelNotice,
			mcp.LoggingLevelWarning, mcp.LoggingLevelError, mcp.LoggingLevelCritical,
			mcp.LoggingLevelAlert, mcp.LoggingLevelEmergency:
			sess.logLevel = params.Level
			log.InfoContext(ctx, "engine.handle_request.ok", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
			return jsonrpc.NewResultResponse(req.ID, &mcp.EmptyResult{})
		default:
			log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", err.Error()), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid logging level", nil), nil
		}

	}

	return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "not implemented", nil), nil
}

// HandleNotification processes an incoming JSON-RPC notification from a client.
// It verifies the session and republishes the notification to all instances without
// actually handling the notification itself. The actual handling is done in the consumer loop
// for these messages.
func (e *Engine) HandleNotification(ctx context.Context, sessID, userID string, note *jsonrpc.Request) error {
	sess, err := e.loadSession(ctx, sessID, userID)
	if err != nil {
		return err
	}

	bytes, err := json.Marshal(note)
	if err != nil {
		e.log.Error("failed to marshal notification", "error", err)
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	// We got a notification from a transport. We don't directly handle notifications in the engine. Instead
	// we dispatch it across the session host so that all instances can see it. The actual handling of
	// notifications is done in the consumer loop for these messages.
	if err := e.host.PublishEvent(ctx, sess.SessionID(), topicForSessionFanout(sess.SessionID()), bytes); err != nil {
		e.log.Error("failed to publish notification", "error", err)
		return fmt.Errorf("failed to publish notification: %w", err)
	}

	return nil
}

func (e *Engine) createSession(ctx context.Context, userID string, protocolVersion string, caps sessions.CapabilitySet, meta sessions.MetadataClientInfo) (*SessionHandle, error) {
	if userID == "" { // user scoping required for auth boundary
		return nil, ErrInvalidUserID
	}
	start := time.Now()
	sid, err := newSessionID()
	if err != nil { // extremely unlikely; fallback to counter-based id
		e.log.ErrorContext(ctx, "engine.create_session.fail", slog.String("user_id", userID), slog.String("err", err.Error()))
		return nil, ErrInternal
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
		TTL:             e.sessionTTL,
		MaxLifetime:     e.sessionMaxLifetime,
		Revoked:         false,
	}
	if err := e.host.CreateSession(ctx, metaRec); err != nil {
		e.log.ErrorContext(ctx, "engine.create_session.fail", slog.String("session_id", sid), slog.String("user_id", userID), slog.String("err", err.Error()))
		return nil, fmt.Errorf("create session: %w", err)
	}
	e.log.InfoContext(ctx, "engine.create_session.ok",
		slog.String("session_id", sid),
		slog.String("user_id", userID),
		slog.String("protocol_version", protocolVersion),
		slog.Bool("cap_sampling", caps.Sampling),
		slog.Bool("cap_roots", caps.Roots),
		slog.Bool("cap_roots_list_changed", caps.RootsListChanged),
		slog.Bool("cap_elicitation", caps.Elicitation),
		slog.Duration("dur", time.Since(start)),
	)

	opts := []SessionHandleOption{}
	if metaRec.Capabilities.Sampling {
		// TODO: Wire up sampling capability here.
	}
	if metaRec.Capabilities.Roots {
		// TODO: Wire up roots capability here.
	}
	if metaRec.Capabilities.Elicitation {
		// TODO: Wire up elicitation capability here.
	}

	return NewSessionHandle(e.host, metaRec, opts...), nil
}

func (e *Engine) loadSession(ctx context.Context, sessID, userID string) (*SessionHandle, error) {
	start := time.Now()
	metaRec, err := e.host.GetSession(ctx, sessID)
	if err != nil {
		e.log.InfoContext(ctx, "engine.load_session.fail", slog.String("session_id", sessID), slog.String("user_id", userID), slog.String("err", err.Error()))
		return nil, err
	}
	if metaRec.Revoked || metaRec.UserID == "" || metaRec.UserID != userID {
		e.log.InfoContext(ctx, "engine.load_session.denied", slog.String("session_id", sessID), slog.String("user_id", userID))
		return nil, ErrSessionNotFound
	}
	// Best-effort sliding TTL touch.
	_ = e.host.TouchSession(ctx, sessID)

	e.log.InfoContext(ctx, "engine.load_session.ok", slog.String("session_id", sessID), slog.String("user_id", userID), slog.Duration("dur", time.Since(start)))

	opts := []SessionHandleOption{}
	if metaRec.Capabilities.Sampling {
		// TODO: Wire up sampling capability here.
	}
	if metaRec.Capabilities.Roots {
		// TODO: Wire up roots capability here.
	}
	if metaRec.Capabilities.Elicitation {
		// TODO: Wire up elicitation capability here.
	}

	return NewSessionHandle(e.host, metaRec, opts...), nil
}

func topicForSessionFanout(sessID string) string {
	return fmt.Sprintf("session:%s:events", sessID)
}

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
