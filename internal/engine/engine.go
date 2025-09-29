package engine

import (
	"context"
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
	"github.com/google/uuid"
)

const (
	defaultSessionTTL         = 1 * time.Hour
	defaultSessionMaxLifetime = 24 * time.Hour
)

const (
	sessionFanoutTopic = "session:events"
)

// internal fanout-only method name for session deletion notifications.
const internalSessionDeletedMethod = "internal/session/deleted"

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
	id   string // process-unique engine ID for coordination

	// session config
	sessionTTL         time.Duration
	sessionMaxLifetime time.Duration
	handshakeTTL       time.Duration

	// tool call tracking
	toolCtxMu      sync.Mutex
	toolCtxCancels map[string]context.CancelCauseFunc // reqID -> cancel func

	// rendez-vous tracking
	rdvMu      sync.Mutex
	rdvChans   map[string]chan []byte // reqID -> response channel
	rdvClosers map[string]func()      // reqID -> close function

	// wiring state for per-session background emitters
	wireMu sync.Mutex
	wired  map[string]bool // sessionID -> registered

	// subscription tracking: sessionID -> uri -> cancel
	subMu      sync.Mutex
	subCancels map[string]map[string]mcpservice.CancelSubscription
}

func NewEngine(host sessions.SessionHost, srv mcpservice.ServerCapabilities, opts ...EngineOption) *Engine {
	e := &Engine{
		host:               host,
		srv:                srv,
		log:                slog.Default(),
		id:                 uuid.NewString(),
		sessionTTL:         defaultSessionTTL,
		sessionMaxLifetime: defaultSessionMaxLifetime,
		handshakeTTL:       30 * time.Second,
		toolCtxCancels:     make(map[string]context.CancelCauseFunc),
		wired:              make(map[string]bool),
		subCancels:         make(map[string]map[string]mcpservice.CancelSubscription),
	}
	// Keep linters honest about method usage in builds where certain paths are pruned.
	_ = e.handshakeTTL
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

// WithHandshakeTTL sets the TTL for a pending session awaiting the client's
// notifications/initialized message. Default is 30s.
func WithHandshakeTTL(d time.Duration) EngineOption {
	return func(m *Engine) {
		if d > 0 {
			m.handshakeTTL = d
		}
	}
}

// (Removed) internal unsubscribe ACK deadline option: waits are governed solely by the request context.

// WithLogger sets a custom logger for the Engine.
func WithLogger(l *slog.Logger) EngineOption {
	return func(m *Engine) {
		if l != nil {
			m.log = l
		}
	}
}

func (e *Engine) Run(ctx context.Context) error {
	// Subscribe to the cross-instance fanout topic and keep the subscription
	// alive for the lifetime of ctx. The host's SubscribeEvents typically
	// returns immediately after spawning its own processing goroutine, so we
	// must not exit here or the derived context would be canceled, tearing down
	// the subscription prematurely.
	if err := e.host.SubscribeEvents(ctx, sessionFanoutTopic, e.handleSessionEvent); err != nil {
		return err
	}

	// Block until shutdown.
	<-ctx.Done()
	return ctx.Err()
}

// InitializeSession handles the MCP initialize handshake, creating a session record,
// wiring negotiated capabilities, and returning the InitializeResult payload alongside
// a session handle for subsequent requests.
func (e *Engine) InitializeSession(ctx context.Context, userID string, req *mcp.InitializeRequest) (*SessionHandle, *mcp.InitializeResult, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("initialize request required")
	}

	negotiatedVersion := req.ProtocolVersion
	if v, ok, err := e.srv.GetPreferredProtocolVersion(ctx); err != nil {
		return nil, nil, fmt.Errorf("get preferred protocol version: %w", err)
	} else if ok && v != "" {
		negotiatedVersion = v
	}

	capSet := sessions.CapabilitySet{}
	if req.Capabilities.Sampling != nil {
		capSet.Sampling = true
	}
	if req.Capabilities.Roots != nil {
		capSet.Roots = true
		capSet.RootsListChanged = req.Capabilities.Roots.ListChanged
	}
	if req.Capabilities.Elicitation != nil {
		capSet.Elicitation = true
	}

	meta := sessions.MetadataClientInfo{
		Name:    req.ClientInfo.Name,
		Version: req.ClientInfo.Version,
	}

	sess, err := e.createSession(ctx, userID, negotiatedVersion, capSet, meta)
	if err != nil {
		return nil, nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = e.host.DeleteSession(ctx, sess.SessionID())
		}
	}()

	serverInfo, err := e.srv.GetServerInfo(ctx, sess)
	if err != nil {
		return nil, nil, fmt.Errorf("get server info: %w", err)
	}

	initRes := &mcp.InitializeResult{
		ProtocolVersion: negotiatedVersion,
		Capabilities:    mcp.ServerCapabilities{},
		ServerInfo:      serverInfo,
	}

	if instr, ok, err := e.srv.GetInstructions(ctx, sess); err != nil {
		return nil, nil, fmt.Errorf("get instructions: %w", err)
	} else if ok {
		initRes.Instructions = instr
	}

	if resCap, ok, err := e.srv.GetResourcesCapability(ctx, sess); err != nil {
		return nil, nil, fmt.Errorf("get resources capability: %w", err)
	} else if ok && resCap != nil {
		entry := &struct {
			ListChanged bool `json:"listChanged"`
			Subscribe   bool `json:"subscribe"`
		}{}
		if subCap, hasSub, subErr := resCap.GetSubscriptionCapability(ctx, sess); subErr != nil {
			return nil, nil, fmt.Errorf("get resources subscription capability: %w", subErr)
		} else if hasSub && subCap != nil {
			entry.Subscribe = true
		}
		if lcCap, hasLC, lcErr := resCap.GetListChangedCapability(ctx, sess); lcErr != nil {
			return nil, nil, fmt.Errorf("get resources listChanged capability: %w", lcErr)
		} else if hasLC && lcCap != nil {
			entry.ListChanged = true
		}
		initRes.Capabilities.Resources = entry
	}

	if toolsCap, ok, err := e.srv.GetToolsCapability(ctx, sess); err != nil {
		return nil, nil, fmt.Errorf("get tools capability: %w", err)
	} else if ok && toolsCap != nil {
		entry := &struct {
			ListChanged bool `json:"listChanged"`
		}{}
		if lcCap, hasLC, lcErr := toolsCap.GetListChangedCapability(ctx, sess); lcErr != nil {
			return nil, nil, fmt.Errorf("get tools listChanged capability: %w", lcErr)
		} else if hasLC && lcCap != nil {
			entry.ListChanged = true
		}
		initRes.Capabilities.Tools = entry
	}

	if promptsCap, ok, err := e.srv.GetPromptsCapability(ctx, sess); err != nil {
		return nil, nil, fmt.Errorf("get prompts capability: %w", err)
	} else if ok && promptsCap != nil {
		entry := &struct {
			ListChanged bool `json:"listChanged"`
		}{}
		if lcCap, hasLC, lcErr := promptsCap.GetListChangedCapability(ctx, sess); lcErr != nil {
			return nil, nil, fmt.Errorf("get prompts listChanged capability: %w", lcErr)
		} else if hasLC && lcCap != nil {
			entry.ListChanged = true
		}
		initRes.Capabilities.Prompts = entry
	}

	if _, ok, err := e.srv.GetLoggingCapability(ctx, sess); err != nil {
		return nil, nil, fmt.Errorf("get logging capability: %w", err)
	} else if ok {
		initRes.Capabilities.Logging = &struct{}{}
	}

	if _, ok, err := e.srv.GetCompletionsCapability(ctx, sess); err != nil {
		return nil, nil, fmt.Errorf("get completions capability: %w", err)
	} else if ok {
		initRes.Capabilities.Completions = &struct{}{}
	}

	cleanup = false

	return sess, initRes, nil
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

	// Require session to be open before serving requests (except initialize, which
	// doesn't reach here).
	if st := sess.State(); st != "" && st != sessions.SessionStateOpen {
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidRequest, "session not initialized", nil), nil
	}

	ctx = logctx.WithSessionID(ctx, sess.SessionID())
	ctx = logctx.WithUserID(ctx, sess.userID)

	switch req.Method {
	case string(mcp.ToolsListMethod):
		return e.handleToolsList(ctx, sess, req)
	case string(mcp.ResourcesListMethod):
		return e.handleResourcesList(ctx, sess, req)
	case string(mcp.ResourcesReadMethod):
		return e.handleResourcesRead(ctx, sess, req)
	case string(mcp.ResourcesTemplatesListMethod):
		return e.handleResourcesTemplatesList(ctx, sess, req)
	case string(mcp.ResourcesSubscribeMethod):
		return e.handleResourcesSubscribe(ctx, sess, req)
	case string(mcp.ResourcesUnsubscribeMethod):
		return e.handleResourcesUnsubscribe(ctx, sess, req)
	case string(mcp.PromptsListMethod):
		return e.handlePromptsList(ctx, sess, req)
	case string(mcp.PromptsGetMethod):
		return e.handlePromptsGet(ctx, sess, req)
	case string(mcp.CompletionCompleteMethod):
		return e.handleCompletionsComplete(ctx, sess, req)
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
		// Invalid level is a client error -> InvalidParams
		if errors.Is(err, mcpservice.ErrInvalidLoggingLevel) {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
		}
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}

	return jsonrpc.NewResultResponse(req.ID, &mcp.EmptyResult{})
}

// handleResourcesSubscribe wires a per-URI subscription via provider and stores a cancel.
func (e *Engine) handleResourcesSubscribe(ctx context.Context, sess *SessionHandle, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	start := time.Now()
	log := e.log.With(slog.String("method", req.Method))

	var params mcp.SubscribeRequest
	if err := json.Unmarshal(req.Params, &params); err != nil || params.URI == "" {
		log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", "invalid params"), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
	}

	resCap, ok, err := e.srv.GetResourcesCapability(ctx, sess)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	if !ok || resCap == nil {
		log.InfoContext(ctx, "engine.handle_request.unsupported", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
	}
	subCap, hasSub, err := resCap.GetSubscriptionCapability(ctx, sess)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	if !hasSub || subCap == nil {
		log.InfoContext(ctx, "engine.handle_request.unsupported", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "subscriptions not supported", nil), nil
	}

	// Idempotency: if already subscribed, succeed.
	e.subMu.Lock()
	if _, ok := e.subCancels[sess.SessionID()]; !ok {
		e.subCancels[sess.SessionID()] = make(map[string]mcpservice.CancelSubscription)
	}
	if _, exists := e.subCancels[sess.SessionID()][params.URI]; exists {
		e.subMu.Unlock()
		return jsonrpc.NewResultResponse(req.ID, &mcp.EmptyResult{})
	}
	e.subMu.Unlock()

	// Build emit closure: publishes notifications/resources/updated.
	// We intentionally do not gate on local subscription bookkeeping to avoid
	// races that could drop early updates emitted immediately after Subscribe
	// returns but before subCancels is populated. Unsubscribe semantics are
	// enforced by cancelling the provider's forwarder, which stops emissions.
	emit := func(cbCtx context.Context, uri string) {
		note := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ResourcesUpdatedNotificationMethod)}
		// attach params
		b, _ := json.Marshal(mcp.ResourceUpdatedNotification{URI: uri})
		note.Params = b
		bytes, err := json.Marshal(note)
		if err != nil {
			log.ErrorContext(ctx, "engine.resources.subscribe.emit_encode_fail", slog.String("err", err.Error()))
			return
		}
		if _, err := e.host.PublishSession(context.WithoutCancel(cbCtx), sess.SessionID(), bytes); err != nil {
			log.ErrorContext(ctx, "engine.resources.subscribe.emit_fail", slog.String("err", err.Error()))
		}
	}

	cancel, err := subCap.Subscribe(ctx, sess, params.URI, emit)
	if err != nil {
		// Treat not found or validation as InvalidParams if detectable; otherwise internal error.
		log.InfoContext(ctx, "engine.handle_request.subscribe.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
	}

	e.subMu.Lock()
	e.subCancels[sess.SessionID()][params.URI] = cancel
	e.subMu.Unlock()

	log.InfoContext(ctx, "engine.handle_request.ok", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
	return jsonrpc.NewResultResponse(req.ID, &mcp.EmptyResult{})
}

// handleResourcesUnsubscribe cancels any local subscription and broadcasts a fanout to other nodes.
func (e *Engine) handleResourcesUnsubscribe(ctx context.Context, sess *SessionHandle, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	start := time.Now()
	log := e.log.With(slog.String("method", req.Method))

	var params mcp.UnsubscribeRequest
	if err := json.Unmarshal(req.Params, &params); err != nil || params.URI == "" {
		log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", "invalid params"), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
	}

	// Publish an unsubscribe request via fanout and return immediately. We do not
	// perform local cancellation here; all nodes (including this one) handle
	// the unsubscribe uniformly when consuming the fanout message.
	fanReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ResourcesUnsubscribeMethod)}
	if b, err := json.Marshal(params); err == nil {
		fanReq.Params = b
	}
	if bytes, err := json.Marshal(fanReq); err == nil {
		if env, err := json.Marshal(fanoutMessage{SessionID: sess.SessionID(), UserID: sess.UserID(), Msg: bytes}); err == nil {
			_ = e.host.PublishEvent(context.WithoutCancel(ctx), sessionFanoutTopic, env)
		}
	}

	log.InfoContext(ctx, "engine.handle_request.ok", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
	return jsonrpc.NewResultResponse(req.ID, &mcp.EmptyResult{})
}

// getResourceSubToken returns the current subscription token for the given
// session URI, stored in the SessionHost KV. Missing keys return empty string.
// Subscription owner and token helpers removed under eventual semantics.

func (e *Engine) handleToolsList(ctx context.Context, sess *SessionHandle, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	start := time.Now()
	log := e.log.With(slog.String("method", req.Method))

	var params mcp.ListToolsRequest
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", err.Error()), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
		}
	}

	cap, ok, err := e.srv.GetToolsCapability(ctx, sess)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	if !ok || cap == nil {
		log.InfoContext(ctx, "engine.handle_request.unsupported", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "tools capability not supported", nil), nil
	}

	var cursor *string
	if params.Cursor != "" {
		s := params.Cursor
		cursor = &s
	}

	page, err := cap.ListTools(ctx, sess, cursor)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}

	result := &mcp.ListToolsResult{
		Tools: page.Items,
	}
	if page.NextCursor != nil {
		result.NextCursor = *page.NextCursor
	}

	log.InfoContext(ctx, "engine.handle_request.ok", slog.Int64("dur_ms", time.Since(start).Milliseconds()), slog.Int("tool_count", len(page.Items)))

	return jsonrpc.NewResultResponse(req.ID, result)
}

func (e *Engine) handleToolCall(ctx context.Context, sess *SessionHandle, req *jsonrpc.Request, writer MessageWriter) (*jsonrpc.Response, error) {
	start := time.Now()
	log := e.log.With(slog.String("method", req.Method))

	var params mcp.CallToolRequestReceived
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.InfoContext(ctx, "engine.handle_request.invalid", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
	}
	if params.Name == "" {
		log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", "missing tool name"), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
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
		// If the tool was cancelled, surface a JSON-RPC error to the client quickly.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			log.InfoContext(ctx, "engine.handle_request.cancelled", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "cancelled", nil), nil
		}
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}

	log.InfoContext(ctx, "engine.handle_request.ok", slog.Int64("dur_ms", time.Since(start).Milliseconds()))

	return jsonrpc.NewResultResponse(req.ID, res)
}

func (e *Engine) handleResourcesTemplatesList(ctx context.Context, sess *SessionHandle, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	start := time.Now()
	log := e.log.With(slog.String("method", req.Method))

	var params mcp.ListResourceTemplatesRequest
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", err.Error()), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
		}
	}

	cap, ok, err := e.srv.GetResourcesCapability(ctx, sess)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	if !ok || cap == nil {
		log.InfoContext(ctx, "engine.handle_request.unsupported", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
	}

	var cursor *string
	if params.Cursor != "" {
		s := params.Cursor
		cursor = &s
	}

	page, err := cap.ListResourceTemplates(ctx, sess, cursor)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}

	res := &mcp.ListResourceTemplatesResult{ResourceTemplates: page.Items}
	if page.NextCursor != nil {
		res.NextCursor = *page.NextCursor
	}
	log.InfoContext(ctx, "engine.handle_request.ok", slog.Int64("dur_ms", time.Since(start).Milliseconds()), slog.Int("template_count", len(page.Items)))
	return jsonrpc.NewResultResponse(req.ID, res)
}

func (e *Engine) handlePromptsList(ctx context.Context, sess *SessionHandle, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	start := time.Now()
	log := e.log.With(slog.String("method", req.Method))

	var params mcp.ListPromptsRequest
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", err.Error()), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
		}
	}

	cap, ok, err := e.srv.GetPromptsCapability(ctx, sess)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	if !ok || cap == nil {
		log.InfoContext(ctx, "engine.handle_request.unsupported", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "prompts capability not supported", nil), nil
	}

	var cursor *string
	if params.Cursor != "" {
		s := params.Cursor
		cursor = &s
	}

	page, err := cap.ListPrompts(ctx, sess, cursor)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}

	res := &mcp.ListPromptsResult{Prompts: page.Items}
	if page.NextCursor != nil {
		res.NextCursor = *page.NextCursor
	}
	log.InfoContext(ctx, "engine.handle_request.ok", slog.Int64("dur_ms", time.Since(start).Milliseconds()), slog.Int("prompt_count", len(page.Items)))
	return jsonrpc.NewResultResponse(req.ID, res)
}

func (e *Engine) handlePromptsGet(ctx context.Context, sess *SessionHandle, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	start := time.Now()
	log := e.log.With(slog.String("method", req.Method))

	var params mcp.GetPromptRequestReceived
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", err.Error()), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
	}
	if params.Name == "" {
		log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", "missing name"), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
	}

	cap, ok, err := e.srv.GetPromptsCapability(ctx, sess)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	if !ok || cap == nil {
		log.InfoContext(ctx, "engine.handle_request.unsupported", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "prompts capability not supported", nil), nil
	}

	result, err := cap.GetPrompt(ctx, sess, &params)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	log.InfoContext(ctx, "engine.handle_request.ok", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
	return jsonrpc.NewResultResponse(req.ID, result)
}

func (e *Engine) handleCompletionsComplete(ctx context.Context, sess *SessionHandle, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	start := time.Now()
	log := e.log.With(slog.String("method", req.Method))

	var params mcp.CompleteRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", err.Error()), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
	}

	cap, ok, err := e.srv.GetCompletionsCapability(ctx, sess)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	if !ok || cap == nil {
		log.InfoContext(ctx, "engine.handle_request.unsupported", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "completions capability not supported", nil), nil
	}

	result, err := cap.Complete(ctx, sess, &params)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	log.InfoContext(ctx, "engine.handle_request.ok", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
	return jsonrpc.NewResultResponse(req.ID, result)
}

func (e *Engine) handleResourcesList(ctx context.Context, sess *SessionHandle, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	start := time.Now()
	log := e.log.With(slog.String("method", req.Method))

	var params mcp.ListResourcesRequest
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", err.Error()), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
		}
	}

	cap, ok, err := e.srv.GetResourcesCapability(ctx, sess)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	if !ok || cap == nil {
		log.InfoContext(ctx, "engine.handle_request.unsupported", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
	}

	var cursor *string
	if params.Cursor != "" {
		s := params.Cursor
		cursor = &s
	}

	page, err := cap.ListResources(ctx, sess, cursor)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}

	res := &mcp.ListResourcesResult{Resources: page.Items}
	if page.NextCursor != nil {
		res.NextCursor = *page.NextCursor
	}
	log.InfoContext(ctx, "engine.handle_request.ok", slog.Int64("dur_ms", time.Since(start).Milliseconds()), slog.Int("resource_count", len(page.Items)))
	return jsonrpc.NewResultResponse(req.ID, res)
}

func (e *Engine) handleResourcesRead(ctx context.Context, sess *SessionHandle, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	start := time.Now()
	log := e.log.With(slog.String("method", req.Method))

	var params mcp.ReadResourceRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", err.Error()), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
	}
	if params.URI == "" {
		log.InfoContext(ctx, "engine.handle_request.invalid", slog.String("err", "missing uri"), slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", nil), nil
	}

	cap, ok, err := e.srv.GetResourcesCapability(ctx, sess)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}
	if !ok || cap == nil {
		log.InfoContext(ctx, "engine.handle_request.unsupported", slog.Int64("dur_ms", time.Since(start).Milliseconds()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
	}

	contents, err := cap.ReadResource(ctx, sess, params.URI)
	if err != nil {
		log.ErrorContext(ctx, "engine.handle_request.fail", slog.String("err", err.Error()))
		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
	}

	res := &mcp.ReadResourceResult{Contents: contents}
	log.InfoContext(ctx, "engine.handle_request.ok", slog.Int64("dur_ms", time.Since(start).Milliseconds()), slog.Int("content_count", len(contents)))
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

	// If the client signals initialized, open the session immediately on this
	// instance to avoid local races, then fan out for other instances.
	if note.Method == string(mcp.InitializedNotificationMethod) {
		now := time.Now().UTC()
		if err := e.host.MutateSession(ctx, sessID, func(m *sessions.SessionMetadata) error {
			if m == nil || m.Revoked || m.UserID != userID {
				return nil
			}
			// Idempotent: if already open, nothing to do.
			if m.State == sessions.SessionStateOpen {
				return nil
			}
			// Treat empty as pending for newly created sessions; set to open.
			m.State = sessions.SessionStateOpen
			if m.OpenedAt.IsZero() {
				m.OpenedAt = now
			}
			m.TTL = e.sessionTTL
			m.UpdatedAt = now
			m.LastAccess = now
			return nil
		}); err != nil {
			e.log.Error("engine.handle_notification.open.fail", slog.String("session_id", sessID), slog.String("user_id", userID), slog.String("err", err.Error()))
			// Continue to publish fanout even if local mutation failed.
		} else {
			// Best-effort eager wiring; safe to call multiple times.
			_ = e.eagerWireSession(ctx, sessID, userID)
		}
	}

	if note.Method == string(mcp.CancelledNotificationMethod) {
		var params mcp.CancelledNotification
		if err := json.Unmarshal(note.Params, &params); err != nil {
			e.log.Error("engine.handle_notification.cancel.decode", slog.String("session_id", sessID), slog.String("user_id", userID), slog.String("err", err.Error()))
			return nil
		}
		if params.RequestID != nil && !params.RequestID.IsNil() {
			ridStr := params.RequestID.String()
			hadCancel := e.cancelInFlightRequest(ridStr, params.Reason)
			e.log.Info("engine.handle_notification.cancel.dispatched", slog.String("session_id", sessID), slog.String("user_id", userID), slog.String("request_id", ridStr), slog.Bool("had_cancel", hadCancel))
		}
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

	// Trace successful dispatch
	e.log.Info("engine.handle_notification.dispatch", slog.String("session_id", sessID), slog.String("user_id", userID), slog.String("method", note.Method))

	return nil
}

func (e *Engine) createSession(ctx context.Context, userID string, protocolVersion string, caps sessions.CapabilitySet, meta sessions.MetadataClientInfo) (*SessionHandle, error) {
	if userID == "" { // user scoping required for auth boundary
		return nil, ErrInvalidUserID
	}
	start := time.Now()
	sid := uuid.NewString()
	now := time.Now().UTC()
	metaRec := &sessions.SessionMetadata{
		MetaVersion:     1,
		SessionID:       sid,
		UserID:          userID,
		ProtocolVersion: protocolVersion,
		Client:          meta,
		Capabilities:    caps,
		State:           sessions.SessionStatePending,
		CreatedAt:       now,
		UpdatedAt:       now,
		LastAccess:      now,
		TTL:             e.handshakeTTL,
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

	return NewSessionHandle(e.host, metaRec, opts...), nil
}

// eagerWireSession ensures the session has session-scoped writers wired so that
// background capabilities can emit immediately after open.
func (e *Engine) eagerWireSession(ctx context.Context, sessID, userID string) error {
	// We only need a session-scoped writer that delivers to the per-session
	// client-facing stream; requestScopedWriter can be nil here.
	sess, err := e.loadSession(ctx, sessID, userID, nil, NewMessageWriterFunc(
		func(ctx context.Context, msg jsonrpc.Message) error {
			_, err := e.host.PublishSession(ctx, sessID, msg)
			return err
		},
	))
	if err != nil {
		return err
	}
	// Mark wiring intent (ensures wireMu is used at least for state guarding)
	e.wireMu.Lock()
	already := e.wired[sessID]
	e.wireMu.Unlock()
	if already {
		return nil
	}
	return e.registerListChangedEmitters(ctx, sess)
}

// registerListChangedEmitters wires capability change listeners to emit JSON-RPC
// notifications on the per-session client stream. It is idempotent per session.
//
//lint:ignore U1000 used via eagerWireSession
func (e *Engine) registerListChangedEmitters(ctx context.Context, sess *SessionHandle) error {
	sid := sess.SessionID()
	uid := sess.UserID()

	e.wireMu.Lock()
	if e.wired[sid] {
		e.wireMu.Unlock()
		return nil
	}
	e.wired[sid] = true
	e.wireMu.Unlock()

	// Use WithoutCancel to outlive a single request while preserving values for logging/tracing.
	bg := context.WithoutCancel(ctx)

	// helper to publish a no-param notification
	publishNote := func(method mcp.Method) {
		note := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(method)}
		bytes, err := json.Marshal(note)
		if err != nil {
			e.log.Error("engine.emitter.encode.fail", slog.String("session_id", sid), slog.String("user_id", uid), slog.String("method", string(method)), slog.String("err", err.Error()))
			return
		}
		if _, err := e.host.PublishSession(bg, sid, bytes); err != nil {
			e.log.Error("engine.emitter.publish.fail", slog.String("session_id", sid), slog.String("user_id", uid), slog.String("method", string(method)), slog.String("err", err.Error()))
		}
	}

	// Resources listChanged
	if resCap, ok, err := e.srv.GetResourcesCapability(bg, sess); err == nil && ok && resCap != nil {
		if lc, hasLC, lErr := resCap.GetListChangedCapability(bg, sess); lErr == nil && hasLC && lc != nil {
			_, _ = lc.Register(bg, sess, func(cbCtx context.Context, s sessions.Session, uri string) {
				_ = uri // we emit generic listChanged per spec
				publishNote(mcp.ResourcesListChangedNotificationMethod)
			})
		}
	}

	// Tools listChanged
	if toolsCap, ok, err := e.srv.GetToolsCapability(bg, sess); err == nil && ok && toolsCap != nil {
		if lc, hasLC, lErr := toolsCap.GetListChangedCapability(bg, sess); lErr == nil && hasLC && lc != nil {
			_, _ = lc.Register(bg, sess, func(cbCtx context.Context, s sessions.Session) {
				publishNote(mcp.ToolsListChangedNotificationMethod)
			})
		}
	}

	// Prompts listChanged
	if promptsCap, ok, err := e.srv.GetPromptsCapability(bg, sess); err == nil && ok && promptsCap != nil {
		if lc, hasLC, lErr := promptsCap.GetListChangedCapability(bg, sess); lErr == nil && hasLC && lc != nil {
			_, _ = lc.Register(bg, sess, func(cbCtx context.Context, s sessions.Session) {
				publishNote(mcp.PromptsListChangedNotificationMethod)
			})
		}
	}

	return nil
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
			opts = append(opts, WithRootsCapability(&rootsCapability{
				eng:                 e,
				log:                 e.log.With(slog.String("session_id", sessID), slog.String("user_id", userID), slog.String("capability", "roots")),
				sessID:              sessID,
				userID:              userID,
				requestScopedWriter: requestScopedWriter,
				sessionScopedWriter: sessionScopedWriter,
			}))
		}
		if metaRec.Capabilities.Elicitation {
			opts = append(opts, WithElicitationCapability(&elicitationCapability{
				eng:                 e,
				log:                 e.log.With(slog.String("session_id", sessID), slog.String("user_id", userID), slog.String("capability", "elicitation")),
				sessID:              sessID,
				userID:              userID,
				requestScopedWriter: requestScopedWriter,
				sessionScopedWriter: sessionScopedWriter,
			}))
		}
	}

	return NewSessionHandle(e.host, metaRec, opts...), nil
}

func (e *Engine) cancelInFlightRequest(reqID string, reason string) bool {
	if reqID == "" {
		return false
	}

	e.toolCtxMu.Lock()
	cancel, exists := e.toolCtxCancels[reqID]
	e.toolCtxMu.Unlock()

	if exists && cancel != nil {
		cancelReason := reason
		if cancelReason == "" {
			cancelReason = "cancelled"
		}
		cancel(errors.New(cancelReason))
	}

	e.rdvMu.Lock()
	if closer, ok := e.rdvClosers[reqID]; ok && closer != nil {
		delete(e.rdvClosers, reqID)
		closer()
	}
	delete(e.rdvChans, reqID)
	e.rdvMu.Unlock()

	return exists && cancel != nil
}

// handleSessionEvent processes a session-related event message received over
// the inter-instance message bus.
func (e *Engine) handleSessionEvent(ctx context.Context, msg []byte) error {
	var fanout fanoutMessage
	if err := json.Unmarshal(msg, &fanout); err != nil {
		e.log.Error("engine.handle_session_event.err", slog.String("err", err.Error()))
		return nil // ignore malformed messages
	}

	// Trace event receipt
	e.log.Info("engine.handle_session_event.recv", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID))

	var jsonMsg jsonrpc.AnyMessage
	if err := json.Unmarshal(fanout.Msg, &jsonMsg); err != nil {
		e.log.Error("engine.handle_session_event.err", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID), slog.String("err", err.Error()))
		return nil // ignore malformed messages
	}

	req := jsonMsg.AsRequest()
	if req != nil {
		switch req.Method {
		case string(mcp.InitializedNotificationMethod):
			now := time.Now().UTC()
			// Flip session to open (idempotent) for cross-instance coordination.
			if err := e.host.MutateSession(ctx, fanout.SessionID, func(m *sessions.SessionMetadata) error {
				if m == nil || m.Revoked || m.UserID != fanout.UserID {
					return nil
				}
				if m.State == sessions.SessionStateOpen {
					return nil
				}
				m.State = sessions.SessionStateOpen
				if m.OpenedAt.IsZero() {
					m.OpenedAt = now
				}
				m.TTL = e.sessionTTL
				m.UpdatedAt = now
				m.LastAccess = now
				return nil
			}); err != nil {
				e.log.Error("engine.handle_session_event.open.fail", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID), slog.String("err", err.Error()))
				return nil
			}
			// Eager-wire emitters after open.
			_ = e.eagerWireSession(ctx, fanout.SessionID, fanout.UserID)
			e.log.Info("engine.handle_session_event.open", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID))
			return nil
		case internalSessionDeletedMethod:
			// Teardown any local per-session subscriptions.
			e.cancelAllSubscriptionsForSession(fanout.SessionID)
			return nil
		case string(mcp.CancelledNotificationMethod):
			var params mcp.CancelledNotification
			if err := json.Unmarshal(req.Params, &params); err != nil {
				e.log.Error("engine.handle_session_event.err", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID), slog.String("err", err.Error()))
				return nil // ignore malformed messages
			}

			if params.RequestID != nil && !params.RequestID.IsNil() {
				ridStr := params.RequestID.String()
				// Trace cancellation delivery
				e.log.Info("engine.handle_session_event.cancel", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID), slog.String("request_id", ridStr), slog.String("reason", params.Reason))

				hadCancel := e.cancelInFlightRequest(ridStr, params.Reason)
				e.log.Info("engine.handle_session_event.cancel.dispatched", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID), slog.String("request_id", ridStr), slog.Bool("had_cancel", hadCancel))
			}
			return nil
		case string(mcp.ResourcesUnsubscribeMethod):
			var params mcp.UnsubscribeRequest
			if err := json.Unmarshal(req.Params, &params); err != nil {
				e.log.Error("engine.handle_session_event.err", slog.String("session_id", fanout.SessionID), slog.String("user_id", fanout.UserID), slog.String("err", err.Error()))
				return nil
			}
			// Best-effort local cancel triggered by fanout message.
			e.subMu.Lock()
			if m := e.subCancels[fanout.SessionID]; len(m) > 0 {
				if cancel, ok := m[params.URI]; ok && cancel != nil {
					_ = cancel(context.WithoutCancel(ctx))
					delete(m, params.URI)
				}
				if len(m) == 0 {
					delete(e.subCancels, fanout.SessionID)
				}
			}
			e.subMu.Unlock()
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
			default:
				// receiver not ready; drop as per at-least-once semantics
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
	// Small buffer to tolerate brief receiver delays without blocking the fanout loop.
	recvCh := make(chan []byte, 1)
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

// StreamSession validates the session ownership and subscribes the caller to the
// per-session client-facing stream starting after lastEventID. It is a thin
// wrapper over the host that centralizes auth/ownership checks in the Engine.
func (e *Engine) StreamSession(ctx context.Context, sessID, userID, lastEventID string, handler sessions.MessageHandlerFunction) error {
	meta, err := e.host.GetSession(ctx, sessID)
	if err != nil || meta == nil || meta.Revoked || meta.UserID != userID {
		return ErrSessionNotFound
	}
	// Best-effort TTL touch; ignore error
	_ = e.host.TouchSession(ctx, sessID)
	// Best-effort eager wiring so background emitters (e.g., listChanged) can fire
	// as soon as a stream consumer attaches, even if notifications/initialized
	// has not yet been sent by the client.
	_ = e.eagerWireSession(ctx, sessID, userID)
	return e.host.SubscribeSession(ctx, sessID, lastEventID, handler)
}

// HandleClientResponse validates the session ownership and forwards a client
// JSON-RPC response into the Engine rendezvous via the inter-instance fanout.
// This allows any node to satisfy the waiting request, regardless of where the
// response was received.
func (e *Engine) HandleClientResponse(ctx context.Context, sessID, userID string, res *jsonrpc.Response) error {
	if res == nil || res.ID == nil || res.ID.IsNil() {
		return fmt.Errorf("invalid response: missing id")
	}
	meta, err := e.host.GetSession(ctx, sessID)
	if err != nil || meta == nil || meta.Revoked || meta.UserID != userID {
		return ErrSessionNotFound
	}

	payload, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	env, err := json.Marshal(fanoutMessage{SessionID: sessID, UserID: userID, Msg: payload})
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	if err := e.host.PublishEvent(ctx, sessionFanoutTopic, env); err != nil {
		return fmt.Errorf("publish fanout: %w", err)
	}
	return nil
}

// GetSessionProtocolVersion validates ownership and returns the negotiated
// protocol version for the session. Returns ErrSessionNotFound if not owned or
// missing.
func (e *Engine) GetSessionProtocolVersion(ctx context.Context, sessID, userID string) (string, error) {
	meta, err := e.host.GetSession(ctx, sessID)
	if err != nil || meta == nil || meta.Revoked || meta.UserID != userID {
		return "", ErrSessionNotFound
	}
	// Best-effort TTL touch
	_ = e.host.TouchSession(ctx, sessID)
	return meta.ProtocolVersion, nil
}

// DeleteSession validates ownership, returns the session protocol version for
// convenience, and deletes the session from the host. Idempotent at the host
// layer; returns ErrSessionNotFound if not owned or already gone.
func (e *Engine) DeleteSession(ctx context.Context, sessID, userID string) (string, error) {
	meta, err := e.host.GetSession(ctx, sessID)
	if err != nil || meta == nil || meta.Revoked || meta.UserID != userID {
		return "", ErrSessionNotFound
	}
	pv := meta.ProtocolVersion
	// Best-effort: cancel any local subscriptions for this session prior to deletion.
	e.cancelAllSubscriptionsForSession(sessID)

	// Broadcast a fanout event so other instances can cancel their local subscriptions.
	// This uses an internal method name understood only by the engine fanout handler.
	note := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: internalSessionDeletedMethod}
	bytes, _ := json.Marshal(note)
	outer := fanoutMessage{SessionID: sessID, UserID: userID, Msg: bytes}
	if payload, err := json.Marshal(outer); err == nil {
		_ = e.host.PublishEvent(context.WithoutCancel(ctx), sessionFanoutTopic, payload)
	}

	if err := e.host.DeleteSession(ctx, sessID); err != nil {
		return "", fmt.Errorf("delete session: %w", err)
	}
	return pv, nil
}

// PublishToSession validates ownership and appends a JSON-RPC message to the
// per-session client-facing stream. Returns the assigned event ID.
func (e *Engine) PublishToSession(ctx context.Context, sessID, userID string, msg jsonrpc.Message) (string, error) {
	meta, err := e.host.GetSession(ctx, sessID)
	if err != nil || meta == nil || meta.Revoked || meta.UserID != userID {
		return "", ErrSessionNotFound
	}
	// Accept either already-encoded []byte or a struct implementing Message
	var bytes []byte
	switch v := any(msg).(type) {
	case []byte:
		bytes = v
	default:
		b, mErr := json.Marshal(msg)
		if mErr != nil {
			return "", fmt.Errorf("marshal message: %w", mErr)
		}
		bytes = b
	}
	evtID, err := e.host.PublishSession(ctx, sessID, bytes)
	if err != nil {
		return "", fmt.Errorf("publish session: %w", err)
	}
	return evtID, nil
}

// cancelAllSubscriptionsForSession cancels and removes all tracked subscriptions for sessID.
func (e *Engine) cancelAllSubscriptionsForSession(sessID string) {
	e.subMu.Lock()
	if m := e.subCancels[sessID]; len(m) > 0 {
		for uri, cancel := range m {
			if cancel != nil {
				_ = cancel(context.Background())
			}
			delete(m, uri)
		}
		delete(e.subCancels, sessID)
	}
	e.subMu.Unlock()
}
