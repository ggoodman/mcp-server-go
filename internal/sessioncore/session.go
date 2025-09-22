package sessioncore

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	internalelicitation "github.com/ggoodman/mcp-server-go/internal/elicitation"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

var _ sessions.Session = (*SessionHandle)(nil)

type SessionHandle struct {
	id              string
	userID          string
	protocolVersion string
	caps            sessions.CapabilitySet
	host            sessions.SessionHost
	mgr             *Manager

	// directWriter, when set for the lifetime of a single inbound POST request
	// handling path, allows server->client JSON-RPC requests emitted via
	// client capabilities (sampling, roots, elicitation, etc.) to be written
	// directly onto the active SSE stream for that POST request instead of
	// (or before) being persisted to the durable session stream. This enables
	// the "single round trip" pattern where the client can receive and respond
	// without a concurrent GET /mcp stream. If the direct write fails, the
	// caller should fall back to durable PublishSession.
	directWriterMu sync.RWMutex
	directWriter   func(context.Context, []byte) error

	// lazy capability wrappers
	onceSampling sync.Once
	samp         *samplingCap

	onceRoots sync.Once
	roots     *rootsCap

	onceElicit sync.Once
	elic       *elicitationCap
}

// SessionID returns the unique session identifier.
func (s *SessionHandle) SessionID() string { return s.id }

// UserID returns the associated user id (if any) for the session.
func (s *SessionHandle) UserID() string { return s.userID }

// ProtocolVersion returns the negotiated MCP protocol version.
func (s *SessionHandle) ProtocolVersion() string { return s.protocolVersion }

// GetSamplingCapability returns the sampling capability if present.
func (s *SessionHandle) GetSamplingCapability() (cap sessions.SamplingCapability, ok bool) {
	if !s.caps.Sampling {
		return nil, false
	}
	s.onceSampling.Do(func() { s.samp = &samplingCap{h: s} })
	return s.samp, true
}

// GetRootsCapability returns the roots capability if present.
func (s *SessionHandle) GetRootsCapability() (cap sessions.RootsCapability, ok bool) {
	if !s.caps.Roots {
		return nil, false
	}
	s.onceRoots.Do(func() { s.roots = &rootsCap{h: s, supportsListChanged: s.caps.RootsListChanged} })
	return s.roots, true
}

// GetElicitationCapability returns the elicitation capability if present.
func (s *SessionHandle) GetElicitationCapability() (cap sessions.ElicitationCapability, ok bool) {
	if !s.caps.Elicitation {
		return nil, false
	}
	s.onceElicit.Do(func() { s.elic = &elicitationCap{h: s} })
	return s.elic, true
}

// SetDirectWriter installs a temporary direct writer used for opportunistic
// streaming of outbound server->client requests for this session. It returns
// a restore function that MUST be invoked to remove the writer (usually via
// defer). Only one writer is stored; later calls replace the previous one.
func (s *SessionHandle) SetDirectWriter(f func(context.Context, []byte) error) (restore func()) {
	s.directWriterMu.Lock()
	prev := s.directWriter
	s.directWriter = f
	s.directWriterMu.Unlock()
	return func() {
		s.directWriterMu.Lock()
		s.directWriter = prev
		s.directWriterMu.Unlock()
	}
}

// tryDirectWrite attempts to write b using the installed direct writer (if any).
// It returns (true, err) if a writer was present (err may be nil or not). When
// ok==false, no writer was installed and the caller should fall back to durable
// publish semantics.
func (s *SessionHandle) tryDirectWrite(ctx context.Context, b []byte) (ok bool, err error) {
	s.directWriterMu.RLock()
	f := s.directWriter
	s.directWriterMu.RUnlock()
	if f == nil {
		return false, nil
	}
	return true, f(ctx, b)
}

// --- Sampling capability wrapper ---

type samplingCap struct{ h *SessionHandle }

func (c *samplingCap) CreateMessage(ctx context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	if c == nil || c.h == nil || req == nil {
		return nil, fmt.Errorf("invalid sampling request")
	}
	var out mcp.CreateMessageResult
	if err := c.h.mgr.clientCall(ctx, c.h, string(mcp.SamplingCreateMessageMethod), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- Roots capability wrapper ---

type rootsCap struct {
	h                   *SessionHandle
	supportsListChanged bool
	mu                  sync.Mutex
	listeners           []sessions.RootsListChangedListener
	subscribed          bool
	cancelSub           context.CancelFunc
}

func (r *rootsCap) ListRoots(ctx context.Context) (*mcp.ListRootsResult, error) {
	if r == nil || r.h == nil {
		return nil, fmt.Errorf("roots capability unavailable")
	}
	var out mcp.ListRootsResult
	if err := r.h.mgr.clientCall(ctx, r.h, string(mcp.RootsListMethod), &mcp.ListRootsRequest{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *rootsCap) RegisterRootsListChangedListener(ctx context.Context, listener sessions.RootsListChangedListener) (bool, error) {
	if !r.supportsListChanged || listener == nil {
		return false, nil
	}
	r.mu.Lock()
	r.listeners = append(r.listeners, listener)
	// First registration sets up subscription to server-internal event (POST handler already publishes by method name)
	if !r.subscribed {
		topic := string(mcp.RootsListChangedNotificationMethod)
		subCtx, cancel := context.WithCancel(context.Background()) // detached parent; stored for later explicit cancellation
		_, err := r.h.host.SubscribeEvents(subCtx, r.h.id, topic, func(c context.Context, _ []byte) error {
			r.notifyListeners(context.Background())
			return nil
		})
		if err != nil {
			r.mu.Unlock()
			cancel()
			return false, err
		}
		// LIFETIME NOTE:
		// We intentionally keep this subscription alive for the entire session lifetime.
		// There is no per-listener unsubscribe; callers register listeners and expect
		// notifications until the session is deleted. The session teardown path
		// (DeleteSession / handler teardown) is responsible for ultimately removing
		// the underlying host subscription; we do not proactively call cancel here.
		// If future requirements demand earlier teardown, invoke r.cancelSub under
		// lock when appropriate.
		r.subscribed = true
		r.cancelSub = cancel
	}
	r.mu.Unlock()
	return true, nil
}

func (r *rootsCap) notifyListeners(ctx context.Context) {
	r.mu.Lock()
	ls := append([]sessions.RootsListChangedListener(nil), r.listeners...)
	r.mu.Unlock()
	for _, fn := range ls {
		_ = fn(ctx)
	}
}

// --- Elicitation capability wrapper ---

type elicitationCap struct{ h *SessionHandle }

func (e *elicitationCap) Elicit(ctx context.Context, text string, target any, opts ...sessions.ElicitOption) (sessions.ElicitAction, error) {
	if e == nil || e.h == nil {
		return "", fmt.Errorf("elicitation unavailable")
	}
	if target == nil {
		return "", fmt.Errorf("nil target")
	}
	var conf sessions.ElicitConfig
	for _, opt := range opts {
		opt(&conf)
	}
	// Validate target is pointer to struct
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return "", fmt.Errorf("target must be pointer to struct")
	}
	t := rv.Elem().Type()
	schema, cp, err := internalelicitation.ProjectSchema(t)
	if err != nil {
		return "", err
	}
	req := &mcp.ElicitRequest{Message: text, RequestedSchema: *schema}
	var out mcp.ElicitResult
	if err := e.h.mgr.clientCall(ctx, e.h, string(mcp.ElicitationCreateMethod), req, &out); err != nil {
		return "", err
	}
	if err := internalelicitation.DecodeForTyped(cp, target, out.Content, conf.Strict); err != nil {
		return "", err
	}
	if conf.RawDst != nil {
		m := make(map[string]any, len(out.Content))
		for k, v := range out.Content {
			m[k] = v
		}
		*conf.RawDst = m
	}
	switch out.Action {
	case string(sessions.ElicitActionAccept):
		return sessions.ElicitActionAccept, nil
	case string(sessions.ElicitActionDecline):
		return sessions.ElicitActionDecline, nil
	case string(sessions.ElicitActionCancel):
		return sessions.ElicitActionCancel, nil
	default:
		return sessions.ElicitAction(""), nil
	}
}

// --- Reflection helpers (limited) ---

// (removed unused jsonMarshalableStructType and debugJSON helpers)
