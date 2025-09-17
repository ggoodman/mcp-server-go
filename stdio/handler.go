package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// Handler is a single-connection stdio transport that reads JSON-RPC messages
// from an io.Reader and writes responses to an io.Writer. By default, it uses
// os.Stdin and os.Stdout. It authenticates the peer using a UserProvider, which
// defaults to the current OS user ID.
//
// The handler is transport-only; it delegates all MCP semantics to the provided
// mcpservice.ServerCapabilities.
type Handler struct {
	srv mcpservice.ServerCapabilities

	r io.Reader
	w io.Writer
	l *slog.Logger

	userProvider UserProvider

	// Per-session, per-URI forwarders for resources/updated notifications
	subMu      sync.Mutex
	subCancels map[string]map[string]context.CancelFunc // sessionID -> uri -> cancel

	// writeMux for the current Serve invocation (single use per handler)
	wm *writeMux

	// inFlight guards per-request cancellation and progress correlation by JSON-RPC ID
	inFlightMu sync.Mutex
	inFlight   map[string]context.CancelFunc
}

// NewHandler constructs a stdio Handler with defaults and applies options.
func NewHandler(srv mcpservice.ServerCapabilities, opts ...Option) *Handler {
	h := &Handler{
		srv:          srv,
		r:            os.Stdin,
		w:            os.Stdout,
		l:            slog.Default(),
		userProvider: OSUserProvider{},
		subCancels:   make(map[string]map[string]context.CancelFunc),
	}
	for _, opt := range opts {
		opt(h)
	}
	h.inFlight = make(map[string]context.CancelFunc)
	return h
}

// Serve runs the stdio event loop until EOF on the reader or the context is canceled.
// It is safe to call at most once per Handler. Serve is responsible for:
//   - JSON-RPC message framing (newline-delimited)
//   - initialize/initialized lifecycle with the provided ServerCapabilities
//   - routing requests/notifications/responses to the capabilities
//   - writing JSON-RPC responses to the writer
//
// The exact wire-format and behavior mirror the MCP stdio transport described in the
// specs; this function focuses on API shape. Implementation will be added in a
// follow-up.
func (h *Handler) Serve(ctx context.Context) error {
	// Ensure forwarders are cleaned up on exit
	defer h.cancelAllSubscriptions()
	// IO setup
	reader := bufio.NewReader(h.r)
	bw := bufio.NewWriter(h.w)
	wm := &writeMux{w: bw}
	h.wm = wm
	defer func() { h.wm = nil }()
	// Outbound dispatcher for server-initiated RPCs
	disp := newOutboundDispatcher(wm)

	// Identify user via provider
	uid, err := h.userProvider.CurrentUserID()
	if err != nil {
		disp.Close(err)
		return err
	}

	// Session state
	var sess *stdioSession // established after initialize
	initialized := false   // JSON-RPC initialize handshake completed (server replied)
	registered := false    // listChanged callbacks registered after client notifications/initialized

	for {
		// Respect context
		select {
		case <-ctx.Done():
			disp.Close(ctx.Err())
			return ctx.Err()
		default:
		}

		// Read next line (newline-delimited JSON per stdio transport)
		line, rerr := reader.ReadBytes('\n')
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				// Graceful shutdown on EOF. Serialize Flush with writeMux to avoid races.
				_ = wm.flush()
				disp.Close(io.EOF)
				return nil
			}
			// Retry briefly on read timeouts
			var ne net.Error
			if errors.As(rerr, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			disp.Close(rerr)
			return rerr
		}
		if len(line) == 0 || isAllWhitespace(line) {
			continue
		}

		var any jsonrpc.AnyMessage
		if err := json.Unmarshal(line, &any); err != nil {
			// Malformed JSON; per JSON-RPC, we can't reliably respond. Log and continue.
			h.l.Debug("invalid JSON-RPC", slog.String("err", err.Error()))
			continue
		}

		if !initialized {
			// Expect initialize request
			req := any.AsRequest()
			if req == nil || req.Method != string(mcp.InitializeMethod) {
				// Protocol violation: expect initialize. Ignore until we get one.
				h.l.Debug("expected initialize first; ignoring message", slog.String("type", any.Type()), slog.String("method", any.Method))
				continue
			}

			var initReq mcp.InitializeRequest
			if err := json.Unmarshal(req.Params, &initReq); err != nil {
				// Respond with invalid params
				resp := jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid initialize params", nil)
				if werr := wm.writeJSONRPC(resp); werr != nil {
					return werr
				}
				continue
			}

			negotiated := initReq.ProtocolVersion
			// Allow server to prefer a version.
			bs := &bootstrapSession{userID: uid}
			if v, ok, err := h.srv.GetPreferredProtocolVersion(ctx, bs); err == nil && ok && v != "" {
				negotiated = v
			}

			// Construct a session for capability discovery and subsequent requests.
			sess = &stdioSession{userID: uid, proto: negotiated, d: disp}
			// Hydrate client-declared capabilities on the session so server code can use them.
			if initReq.Capabilities.Sampling != nil {
				sess.samp = &stdioSamplingCap{d: disp}
			}
			if initReq.Capabilities.Roots != nil {
				sess.roots = &stdioRootsCap{d: disp, supportsListChanged: initReq.Capabilities.Roots.ListChanged}
			}
			if initReq.Capabilities.Elicitation != nil {
				sess.elic = &stdioElicitCap{d: disp}
			}

			// Build initialize result by probing server capabilities.
			serverInfo, err := h.srv.GetServerInfo(ctx, sess)
			if err != nil {
				resp := jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "failed to get server info", nil)
				if werr := wm.writeJSONRPC(resp); werr != nil {
					return werr
				}
				continue
			}

			initRes := &mcp.InitializeResult{ProtocolVersion: negotiated, ServerInfo: serverInfo}

			// Resources capability
			if resCap, ok, err := h.srv.GetResourcesCapability(ctx, sess); err != nil {
				resp := jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "failed to get resources capability", nil)
				if werr := wm.writeJSONRPC(resp); werr != nil {
					return werr
				}
				continue
			} else if ok && resCap != nil {
				var listChanged, subscribe bool
				if _, ok, err := resCap.GetListChangedCapability(ctx, sess); err == nil {
					listChanged = ok
				}
				if _, ok, err := resCap.GetSubscriptionCapability(ctx, sess); err == nil {
					subscribe = ok
				}
				initRes.Capabilities.Resources = &struct {
					ListChanged bool `json:"listChanged"`
					Subscribe   bool `json:"subscribe"`
				}{ListChanged: listChanged, Subscribe: subscribe}
			}

			// Tools capability
			if toolsCap, ok, err := h.srv.GetToolsCapability(ctx, sess); err != nil {
				resp := jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "failed to get tools capability", nil)
				if werr := wm.writeJSONRPC(resp); werr != nil {
					return werr
				}
				continue
			} else if ok && toolsCap != nil {
				var listChanged bool
				if _, ok, err := toolsCap.GetListChangedCapability(ctx, sess); err == nil {
					listChanged = ok
				}
				initRes.Capabilities.Tools = &struct {
					ListChanged bool `json:"listChanged"`
				}{ListChanged: listChanged}
			}

			// Prompts capability
			if promptsCap, ok, err := h.srv.GetPromptsCapability(ctx, sess); err != nil {
				resp := jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "failed to get prompts capability", nil)
				if werr := wm.writeJSONRPC(resp); werr != nil {
					return werr
				}
				continue
			} else if ok && promptsCap != nil {
				var listChanged bool
				if _, ok, err := promptsCap.GetListChangedCapability(ctx, sess); err == nil {
					listChanged = ok
				}
				initRes.Capabilities.Prompts = &struct {
					ListChanged bool `json:"listChanged"`
				}{ListChanged: listChanged}
			}

			// Optional instructions
			if instr, ok, err := h.srv.GetInstructions(ctx, sess); err == nil && ok {
				initRes.Instructions = instr
			}

			// Logging capability (advertised if supported)
			if _, ok, err := h.srv.GetLoggingCapability(ctx, sess); err == nil && ok {
				if initRes.Capabilities.Logging == nil {
					initRes.Capabilities.Logging = &struct{}{}
				}
			}

			// Completions capability (advertised if supported)
			if _, ok, err := h.srv.GetCompletionsCapability(ctx, sess); err == nil && ok {
				if initRes.Capabilities.Completions == nil {
					initRes.Capabilities.Completions = &struct{}{}
				}
			}

			// Respond
			if resp, err := jsonrpc.NewResultResponse(req.ID, initRes); err != nil {
				r2 := jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "failed to encode initialize result", nil)
				if werr := wm.writeJSONRPC(r2); werr != nil {
					return werr
				}
			} else if werr := wm.writeJSONRPC(resp); werr != nil {
				return werr
			}

			initialized = true
			continue
		}

		// After initialization
		switch any.Type() {
		case "notification":
			// Let dispatcher observe notifications (cancel/progress)
			disp.OnNotification(any)
			// Inbound cancellation from client for server-handled request IDs
			if any.Method == string(mcp.CancelledNotificationMethod) && len(any.Params) > 0 {
				var p struct {
					RequestID string `json:"requestId"`
				}
				if err := json.Unmarshal(any.Params, &p); err == nil && p.RequestID != "" {
					h.inFlightMu.Lock()
					if cancel, ok := h.inFlight[p.RequestID]; ok && cancel != nil {
						cancel()
						delete(h.inFlight, p.RequestID)
					}
					h.inFlightMu.Unlock()
				}
			}
			// Client-originated roots list_changed: invoke registered listeners if present.
			if any.Method == string(mcp.RootsListChangedNotificationMethod) && sess != nil && sess.roots != nil {
				sess.roots.notifyListeners(ctx)
			}
			// notifications/initialized marks that the client completed its side of init.
			// Only after this point should the server begin emitting list_changed notifications.
			if any.Method == string(mcp.InitializedNotificationMethod) && !registered {
				// Register listChanged callbacks where supported. Use Serve context for teardown.
				if resCap, ok, err := h.srv.GetResourcesCapability(ctx, sess); err == nil && ok && resCap != nil {
					if lcap, ok, err := resCap.GetListChangedCapability(ctx, sess); err == nil && ok && lcap != nil {
						_, _ = lcap.Register(ctx, sess, func(_ context.Context, _ sessions.Session, _ string) {
							n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ResourcesListChangedNotificationMethod)}
							_ = h.writeJSONRPC(n)
						})
					}
				}
				if tcap, ok, err := h.srv.GetToolsCapability(ctx, sess); err == nil && ok && tcap != nil {
					if lcap, ok, err := tcap.GetListChangedCapability(ctx, sess); err == nil && ok && lcap != nil {
						_, _ = lcap.Register(ctx, sess, func(_ context.Context, _ sessions.Session) {
							n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ToolsListChangedNotificationMethod)}
							_ = h.writeJSONRPC(n)
						})
					}
				}
				if pcap, ok, err := h.srv.GetPromptsCapability(ctx, sess); err == nil && ok && pcap != nil {
					if lcap, ok, err := pcap.GetListChangedCapability(ctx, sess); err == nil && ok && lcap != nil {
						_, _ = lcap.Register(ctx, sess, func(_ context.Context, _ sessions.Session) {
							n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.PromptsListChangedNotificationMethod)}
							_ = h.writeJSONRPC(n)
						})
					}
				}
				registered = true
			}
			// Other notifications (progress, cancelled, etc.) are accepted and ignored.
			continue
		case "response":
			// Route responses to the outbound dispatcher if any pending call exists.
			if resp := any.AsResponse(); resp != nil {
				disp.OnResponse(resp)
			}
			continue
		case "request":
			req := any.AsRequest()
			if req == nil || req.ID == nil {
				continue
			}
			// Create a per-request context that we can cancel if the client sends notifications/cancelled
			reqCtx, cancel := context.WithCancel(ctx)
			// Track cancel by request ID string
			rid := req.ID.String()
			h.inFlightMu.Lock()
			h.inFlight[rid] = cancel
			h.inFlightMu.Unlock()

			// Inject a ProgressReporter tied to this request ID
			if req.ID != nil && !req.ID.IsNil() {
				pr := stdioProgressReporter{w: wm, requestID: rid}
				reqCtx = mcpservice.WithProgressReporter(reqCtx, pr)
			}

			// Dispatch
			resp, err := h.handleRequest(reqCtx, sess, req)
			// Cleanup inflight entry
			h.inFlightMu.Lock()
			delete(h.inFlight, rid)
			h.inFlightMu.Unlock()
			cancel()
			if err != nil {
				// Map internal error
				r := jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil)
				if werr := wm.writeJSONRPC(r); werr != nil {
					return werr
				}
				continue
			}
			if resp != nil {
				if werr := wm.writeJSONRPC(resp); werr != nil {
					return werr
				}
			}
		}
	}
}

// stdioProgressReporter emits notifications/progress for a given JSON-RPC request ID.
type stdioProgressReporter struct {
	w         jsonrpcWriter
	requestID string
}

func (p stdioProgressReporter) Report(ctx context.Context, progress, total float64) error {
	params := mcp.ProgressNotificationParams{ProgressToken: p.requestID, Progress: progress}
	if total > 0 {
		params.Total = total
	}
	n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ProgressNotificationMethod), Params: mustJSON(params)}
	return p.w.writeJSONRPC(n)
}

// handleRequest routes JSON-RPC requests to server capabilities and produces a response.
func (h *Handler) handleRequest(ctx context.Context, session sessions.Session, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	switch req.Method {
	case string(mcp.PingMethod):
		return jsonrpc.NewResultResponse(req.ID, struct{}{})

	case string(mcp.ToolsListMethod):
		toolsCap, ok, err := h.srv.GetToolsCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || toolsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "tools capability not supported", nil), nil
		}
		var in mcp.ListToolsRequest
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}
		page, err := toolsCap.ListTools(ctx, session, cursorPtr(in.Cursor))
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		return jsonrpc.NewResultResponse(req.ID, &mcp.ListToolsResult{
			Tools: page.Items,
			PaginatedResult: mcp.PaginatedResult{
				NextCursor: deref(page.NextCursor),
			},
		})

	case string(mcp.ToolsCallMethod):
		toolsCap, ok, err := h.srv.GetToolsCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || toolsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "tools capability not supported", nil), nil
		}
		var in mcp.CallToolRequestReceived
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}
		result, err := toolsCap.CallTool(ctx, session, &in)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		return jsonrpc.NewResultResponse(req.ID, result)

	case string(mcp.ResourcesListMethod):
		resCap, ok, err := h.srv.GetResourcesCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || resCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}
		var in mcp.ListResourcesRequest
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}
		page, err := resCap.ListResources(ctx, session, cursorPtr(in.Cursor))
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		return jsonrpc.NewResultResponse(req.ID, &mcp.ListResourcesResult{
			Resources:       page.Items,
			PaginatedResult: mcp.PaginatedResult{NextCursor: deref(page.NextCursor)},
		})

	case string(mcp.ResourcesTemplatesListMethod):
		resCap, ok, err := h.srv.GetResourcesCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || resCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}
		var in mcp.ListResourceTemplatesRequest
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}
		page, err := resCap.ListResourceTemplates(ctx, session, cursorPtr(in.Cursor))
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		return jsonrpc.NewResultResponse(req.ID, &mcp.ListResourceTemplatesResult{
			ResourceTemplates: page.Items,
			PaginatedResult:   mcp.PaginatedResult{NextCursor: deref(page.NextCursor)},
		})

	case string(mcp.ResourcesReadMethod):
		resCap, ok, err := h.srv.GetResourcesCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || resCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}
		var in mcp.ReadResourceRequest
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}
		contents, err := resCap.ReadResource(ctx, session, in.URI)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		return jsonrpc.NewResultResponse(req.ID, &mcp.ReadResourceResult{Contents: contents})

	case string(mcp.ResourcesSubscribeMethod):
		resCap, ok, err := h.srv.GetResourcesCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || resCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}
		subCap, ok, err := resCap.GetSubscriptionCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || subCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources subscriptions not supported", nil), nil
		}
		var in mcp.SubscribeRequest
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}
		if err := subCap.Subscribe(ctx, session, in.URI); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}

		// If the resources capability exposes a per-URI subscriber channel, start a forwarder
		// that emits notifications/resources/updated for this session+URI.
		type uriSubscriber interface {
			SubscriberForURI(uri string) <-chan struct{}
		}
		if us, uok := any(resCap).(uriSubscriber); uok {
			if ch := us.SubscriberForURI(in.URI); ch != nil {
				sid := session.SessionID()
				h.subMu.Lock()
				if _, ok := h.subCancels[sid]; !ok {
					h.subCancels[sid] = make(map[string]context.CancelFunc)
				}
				if _, exists := h.subCancels[sid][in.URI]; !exists {
					// Derive from background so the forwarder outlives this request;
					// it's cancelled explicitly on unsubscribe or Serve teardown.
					fctx, cancel := context.WithCancel(context.Background())
					h.subCancels[sid][in.URI] = cancel
					go func(uri string, ch <-chan struct{}) {
						for {
							select {
							case <-fctx.Done():
								return
							case _, ok := <-ch:
								if !ok {
									return
								}
								// Emit notifications/resources/updated { uri }
								n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ResourcesUpdatedNotificationMethod), Params: mustJSON(&mcp.ResourceUpdatedNotification{URI: uri})}
								_ = h.writeJSONRPC(n)
							}
						}
					}(in.URI, ch)
				}
				h.subMu.Unlock()
			}
		}
		return jsonrpc.NewResultResponse(req.ID, &mcp.EmptyResult{})

	case string(mcp.ResourcesUnsubscribeMethod):
		resCap, ok, err := h.srv.GetResourcesCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || resCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}
		subCap, ok, err := resCap.GetSubscriptionCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || subCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources subscriptions not supported", nil), nil
		}
		var in mcp.UnsubscribeRequest
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}
		if err := subCap.Unsubscribe(ctx, session, in.URI); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		// Tear down forwarder if present
		{
			sid := session.SessionID()
			h.subMu.Lock()
			if m, ok := h.subCancels[sid]; ok {
				if cancel, ok := m[in.URI]; ok {
					cancel()
					delete(m, in.URI)
					if len(m) == 0 {
						delete(h.subCancels, sid)
					}
				}
			}
			h.subMu.Unlock()
		}
		return jsonrpc.NewResultResponse(req.ID, &mcp.EmptyResult{})

	case string(mcp.PromptsListMethod):
		pcap, ok, err := h.srv.GetPromptsCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || pcap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "prompts capability not supported", nil), nil
		}
		var in mcp.ListPromptsRequest
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}
		page, err := pcap.ListPrompts(ctx, session, cursorPtr(in.Cursor))
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		return jsonrpc.NewResultResponse(req.ID, &mcp.ListPromptsResult{
			Prompts:         page.Items,
			PaginatedResult: mcp.PaginatedResult{NextCursor: deref(page.NextCursor)},
		})

	case string(mcp.PromptsGetMethod):
		pcap, ok, err := h.srv.GetPromptsCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || pcap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "prompts capability not supported", nil), nil
		}
		var in mcp.GetPromptRequestReceived
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}
		result, err := pcap.GetPrompt(ctx, session, &in)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		return jsonrpc.NewResultResponse(req.ID, result)

	case string(mcp.LoggingSetLevelMethod):
		lcap, ok, err := h.srv.GetLoggingCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || lcap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "logging capability not supported", nil), nil
		}
		var in mcp.SetLevelRequest
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}
		if !mcp.IsValidLoggingLevel(in.Level) {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid logging level", nil), nil
		}
		if err := lcap.SetLevel(ctx, session, in.Level); err != nil {
			// Map known invalid-level error to Invalid Params; everything else is internal.
			if errors.Is(err, mcpservice.ErrInvalidLoggingLevel) {
				return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid logging level", nil), nil
			}
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		return jsonrpc.NewResultResponse(req.ID, &mcp.EmptyResult{})

	case string(mcp.CompletionCompleteMethod):
		ccap, ok, err := h.srv.GetCompletionsCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || ccap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "completions capability not supported", nil), nil
		}
		var in mcp.CompleteRequest
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}
		result, err := ccap.Complete(ctx, session, &in)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		return jsonrpc.NewResultResponse(req.ID, result)
	}
	return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "method not found", nil), nil
}

// stdioSession is a minimal sessions.Session implementation for stdio transport.
type stdioSession struct {
	userID string
	proto  string
	d      *outboundDispatcher
	// optional client-declared capabilities
	samp  *stdioSamplingCap
	roots *stdioRootsCap
	elic  *stdioElicitCap
}

func (s *stdioSession) SessionID() string       { return fmt.Sprintf("%d", os.Getpid()) }
func (s *stdioSession) UserID() string          { return s.userID }
func (s *stdioSession) ProtocolVersion() string { return s.proto }
func (s *stdioSession) GetSamplingCapability() (sessions.SamplingCapability, bool) {
	if s.samp == nil {
		return nil, false
	}
	return s.samp, true
}
func (s *stdioSession) GetRootsCapability() (sessions.RootsCapability, bool) {
	if s.roots == nil {
		return nil, false
	}
	return s.roots, true
}
func (s *stdioSession) GetElicitationCapability() (sessions.ElicitationCapability, bool) {
	if s.elic == nil {
		return nil, false
	}
	return s.elic, true
}

// --- stdio client capability implementations ---

type stdioSamplingCap struct{ d *outboundDispatcher }

func (c *stdioSamplingCap) CreateMessage(ctx context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	if c == nil || c.d == nil || req == nil {
		return nil, fmt.Errorf("invalid sampling request")
	}
	resp, err := c.d.Call(ctx, string(mcp.SamplingCreateMessageMethod), req)
	if err != nil {
		return nil, err
	}
	var out mcp.CreateMessageResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type stdioRootsCap struct {
	d                   *outboundDispatcher
	supportsListChanged bool
	mu                  sync.Mutex
	listeners           []sessions.RootsListChangedListener
}

func (r *stdioRootsCap) ListRoots(ctx context.Context) (*mcp.ListRootsResult, error) {
	if r == nil || r.d == nil {
		return nil, fmt.Errorf("roots capability unavailable")
	}
	resp, err := r.d.Call(ctx, string(mcp.RootsListMethod), &mcp.ListRootsRequest{})
	if err != nil {
		return nil, err
	}
	var out mcp.ListRootsResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *stdioRootsCap) RegisterRootsListChangedListener(ctx context.Context, listener sessions.RootsListChangedListener) (bool, error) {
	if !r.supportsListChanged || listener == nil {
		return false, nil
	}
	r.mu.Lock()
	r.listeners = append(r.listeners, listener)
	r.mu.Unlock()
	return true, nil
}

func (r *stdioRootsCap) notifyListeners(ctx context.Context) {
	r.mu.Lock()
	ls := append([]sessions.RootsListChangedListener(nil), r.listeners...)
	r.mu.Unlock()
	for _, fn := range ls {
		_ = fn(ctx)
	}
}

type stdioElicitCap struct{ d *outboundDispatcher }

func (e *stdioElicitCap) Elicit(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	if e == nil || e.d == nil || req == nil {
		return nil, fmt.Errorf("invalid elicitation request")
	}
	resp, err := e.d.Call(ctx, string(mcp.ElicitationCreateMethod), req)
	if err != nil {
		return nil, err
	}
	var out mcp.ElicitResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// bootstrapSession is used only for version preference probe during initialize.
type bootstrapSession struct{ userID string }

func (b *bootstrapSession) SessionID() string       { return "" }
func (b *bootstrapSession) UserID() string          { return b.userID }
func (b *bootstrapSession) ProtocolVersion() string { return "" }
func (b *bootstrapSession) GetSamplingCapability() (sessions.SamplingCapability, bool) {
	return nil, false
}
func (b *bootstrapSession) GetRootsCapability() (sessions.RootsCapability, bool) { return nil, false }
func (b *bootstrapSession) GetElicitationCapability() (sessions.ElicitationCapability, bool) {
	return nil, false
}

// writeMux serializes writes and ensures newline framing.
type writeMux struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func (w *writeMux) writeJSONRPC(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.w.Write(b); err != nil {
		return err
	}
	if err := w.w.WriteByte('\n'); err != nil {
		return err
	}
	return w.w.Flush()
}

// flush serializes a direct Flush on the underlying writer to avoid races with writeJSONRPC.
func (w *writeMux) flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Flush()
}

func isAllWhitespace(b []byte) bool {
	for _, c := range b {
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			return false
		}
	}
	return true
}

func cursorPtr(s string) *string {
	if s == "" {
		return nil
	}
	cp := s
	return &cp
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// writeJSONRPC writes using the handler's writeMux if available.
func (h *Handler) writeJSONRPC(v any) error {
	if h.wm == nil {
		return fmt.Errorf("writer not initialized")
	}
	return h.wm.writeJSONRPC(v)
}

// cancelAllSubscriptions stops all active per-URI forwarders.
func (h *Handler) cancelAllSubscriptions() {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	for sid, m := range h.subCancels {
		for _, cancel := range m {
			cancel()
		}
		delete(h.subCancels, sid)
	}
}
