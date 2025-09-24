package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/internal/logctx"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"golang.org/x/sync/errgroup"
)

const (
	defaultSessionTTL         = 1 * time.Hour
	defaultSessionMaxLifetime = 24 * time.Hour
)

const (
	sessionFanoutTopic = "session:events"
)

var (
	ErrCancelled       = errors.New("operation cancelled")
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

	// tool call tracking
	toolCtxMu      sync.Mutex
	toolCtxCancels map[string]context.CancelCauseFunc // reqID -> cancel func

	// rendez-vous tracking
	rdvMu      sync.Mutex
	rdvChans   map[string]chan []byte // reqID -> response channel
	rdvClosers map[string]func()      // reqID -> close function
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

func (e *Engine) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return e.host.SubscribeEvents(ctx, sessionFanoutTopic, e.handleSessionEvent)
	})

	return g.Wait()
}

func (e *Engine) HandleRequest(ctx context.Context, sessID, userID string, req *jsonrpc.Request, requestScopedWriter MessageWriter) (*jsonrpc.Response, error) {
	sess, err := e.loadSession(ctx, sessID, userID, requestScopedWriter, NewMessageWriterFunc(
		func(ctx context.Context, msg jsonrpc.Message) error {
			_, err := e.host.PublishSession(ctx, sessID, msg)
			return err
		},
	))
	if err != nil {
		return nil, err
	}

	ctx = logctx.WithSessionID(ctx, sess.SessionID())
	ctx = logctx.WithUserID(ctx, sess.userID)

	switch req.Method {
	case string(mcp.LoggingSetLevelMethod):
		return e.handleSetLoggingLevel(ctx, sess, req)
	case string(mcp.ToolsCallMethod):
		return e.handleToolCall(ctx, sess, req, requestScopedWriter)
	}

	return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "not implemented", nil), nil
}

func (e *Engine) handleSetLoggingLevel(ctx context.Context, sess *SessionHandle, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	start := time.Now()
	log := e.log.With(slog.String("method", req.Method))
	var params mcp.SetLevelRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", err.Error()), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
	}

	cap, ok, err := e.srv.GetLoggingCapability(ctx, sess)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	if !ok {
		log.InfoContext(ctx, "engine.handle_request.unsupported", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "logging level not supported", nil), nil
	}
	if cap == nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", "nil capability"))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}

	if err := cap.SetLevel(ctx, sess, params.Level); err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}

	return jsonrpc.NewResultResponse(req.ID, &mcp.EmptyResult{})
}

func (e *Engine) handleToolCall(ctx context.Context, sess *SessionHandle, req *jsonrpc.Request, writer MessageWriter) (*jsonrpc.Response, error) {
	start := time.Now()
	log := e.log.With(slog.String("method", req.Method))

	var params mcp.CallToolRequestReceived
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.InfoContext(ctx, "engine.handle_request.invalid", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
	}

	cap, ok, err := e.srv.GetToolsCapability(ctx, sess)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	if !ok {
		log.InfoContext(ctx, "engine.handle_request.unsupported", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "logging level not supported", nil), nil
	}
	if cap == nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", "nil capability"))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}

	// Before delegating to the tools capability, we need to set up up some local tracking state.
	// Any instance may receive a cancellation notification that will get routed through the
	// host's internal event system. We need to set up a rendez-vous so that when the consumer loop
	// sees a cancellation, it can find the right context to cancel.

	reqID := req.ID.String()
	if reqID == "" {
		// This should never happen; the JSON-RPC library should enforce this.
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", "missing request ID"))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidRequest, "missing request ID", nil), nil
	}

	toolCtx, toolCancel := context.WithCancelCause(ctx)
	defer toolCancel(context.Canceled)

	e.toolCtxMu.Lock()
	if _, exists := e.toolCtxCancels[reqID]; exists {
		// This should never happen; request IDs are unique per session.
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", "duplicate request ID"))
		e.toolCtxMu.Unlock()
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	e.toolCtxCancels[reqID] = toolCancel
	e.toolCtxMu.Unlock()

	defer func() {
		e.toolCtxMu.Lock()
		delete(e.toolCtxCancels, reqID)
		e.toolCtxMu.Unlock()
	}()

	res, err := cap.CallTool(toolCtx, sess, &params)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}

	log.InfoContext(ctx, "engine.handle_request.ok", slog.Int64("dur_ms", time.Since(start).Milliseconds()))

	return jsonrpc.NewResultResponse(req.ID, res)
}

// HandleNotification processes an incoming JSON-RPC notification from a client.
// It verifies the session and republishes the notification to all instances without
// actually handling the notification itself. The actual handling is done in the consumer loop
// for these messages.
func (e *Engine) HandleNotification(ctx context.Context, sessID, userID string, note *jsonrpc.Request) error {
	_, err := e.loadSession(ctx, sessID, userID, nil, nil)
	if err != nil {
		return err
	}

	// We got a notification from a transport. We don't directly handle notifications in the engine. Instead
	// we dispatch it across the session host so that all instances can see it. The actual handling of
	// notifications is done in the consumer loop for these messages.
	noteBytes, err := json.Marshal(note)
	if err != nil {
		e.log.Error("engine.handle_notification.err", slog.String("session_id", sessID), slog.String("user_id", userID), slog.String("err", err.Error()))
		return err
	}
	msg, err := json.Marshal(fanoutMessage{
		SessionID: sessID,
		UserID:    userID,
		Msg:       noteBytes,
	})
	if err != nil {
		e.log.Error("engine.handle_notification.err", slog.String("session_id", sessID), slog.String("user_id", userID), slog.String("err", err.Error()))
		return err
	}

	if err := e.host.PublishEvent(ctx, sessionFanoutTopic, msg); err != nil {
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

// loadSession retrieves and validates session metadata, returning a handle.
// It verifies the session belongs to the specified user and is not revoked.
// It also performs a best-effort TTL touch. If a writers are provided, it sets
// up capabilities according to the session's negotiated capabilities and
// leverages the relevant writers for outbound messages.
func (e *Engine) loadSession(ctx context.Context, sessID, userID string, requestScopedWriter MessageWriter, sessionScopedWriter MessageWriter) (*SessionHandle, error) {
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

	if requestScopedWriter != nil && sessionScopedWriter != nil {
		if metaRec.Capabilities.Sampling {
			opts = append(opts, WithSamplingCapability(&samplingCapabilty{
				eng:                 e,
				log:                 e.log.With(slog.String("session_id", sessID), slog.String("user_id", userID), slog.String("capability", "sampling")),
				sessID:              sessID,
				userID:              userID,
				requestScopedWriter: requestScopedWriter,
				sessionScopedWriter: sessionScopedWriter,
			}))
		}
		if metaRec.Capabilities.Roots {
			// TODO: Wire up roots capability here.
		}
		if metaRec.Capabilities.Elicitation {
			// TODO: Wire up elicitation capability here.
		}
	}

	return NewSessionHandle(e.host, metaRec, opts...), nil
}

func (e *Engine) cancelToolCall(reqID string, reason string) {
	e.toolCtxMu.Lock()
	cancel, exists := e.toolCtxCancels[reqID]
	e.toolCtxMu.Unlock()
	if exists && cancel != nil {
		cancel(errors.New(reason))
	}
}

// handleSessionEvent processes a session-related event message received over
// the inter-instance message bus.
func (e *Engine) handleSessionEvent(ctx context.Context, msg []byte) error {
	var fanout fanoutMessage
	if err := json.Unmarshal(msg, &fanout); err != nil {
		e.log.Error("engine.handle_session_event.err", slog.String("err", err.Error()))
		return nil // ignore malformed messages
	}

	var jsonMsg jsonrpc.AnyMessage
	if err := json.Unmarshal(fanout.Msg, &jsonMsg); err != nil {
		e.log.Error("engine.handle_session_event.err", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID), slog.String("err", err.Error()))
		return nil // ignore malformed messages
	}

	req := jsonMsg.AsRequest()
	if req != nil {
		switch req.Method {
		case string(mcp.CancelledNotificationMethod):
			var params mcp.CancelledNotification
			if err := json.Unmarshal(req.Params, &params); err != nil {
				e.log.Error("engine.handle_session_event.err", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID), slog.String("err", err.Error()))
				return nil // ignore malformed messages
			}

			e.cancelToolCall(params.RequestID, params.Reason)
			return nil
		default:
			// Unknown request; ignore.
			e.log.Info("engine.handle_session_event.invalid", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID), slog.String("method", req.Method))
			return nil
		}
	}

	res := jsonMsg.AsResponse()
	if res != nil {
		// Responses will typically satisfy a rendez-vous channel.
		reqID := res.ID.String()
		if reqID == "" {
			e.log.Error("engine.handle_session_event.invalid", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID), slog.String("err", "empty request ID"))
			return nil // ignore malformed messages
		}

		e.rdvMu.Lock()
		rdvCh, exists := e.rdvChans[reqID]
		e.rdvMu.Unlock()
		if exists && rdvCh != nil {
			// Best-effort send; if the channel is blocked or closed, just drop the message.
			select {
			case rdvCh <- fanout.Msg:
			case <-ctx.Done():
			}
			return nil
		} else {
			e.log.Info("engine.handle_session_event.invalid", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID), slog.String("request_id", reqID))
			return nil
		}
	}

	return nil
}

// createRendezVous creates a rendez-vous channel for a given request ID. The
// returned channel will receive a single message (the response) and then be closed.
// The returned close function MUST be called to clean up resources associated
// with the rendez-vous once it is no longer needed.
func (e *Engine) createRendezVous(reqID string) (<-chan []byte, func()) {
	recvCh := make(chan []byte)
	closeCh := func() {
		close(recvCh)
	}

	e.rdvMu.Lock()
	if e.rdvChans == nil {
		e.rdvChans = make(map[string]chan []byte)
	}
	if e.rdvClosers == nil {
		e.rdvClosers = make(map[string]func())
	}
	e.rdvChans[reqID] = recvCh
	e.rdvClosers[reqID] = closeCh
	e.rdvMu.Unlock()

	return recvCh, func() {
		e.rdvMu.Lock()
		if closer, exists := e.rdvClosers[reqID]; exists && closer != nil {
			delete(e.rdvClosers, reqID)
			closer()
		}
		delete(e.rdvChans, reqID)
		e.rdvMu.Unlock()
	}
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

// newClientMessageID returns a cryptographically random client message id with a prefix.
func newClientMessageID() (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	return "req-" + id, nil
}
