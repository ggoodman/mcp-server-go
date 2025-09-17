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
}

// NewHandler constructs a stdio Handler with defaults and applies options.
func NewHandler(srv mcpservice.ServerCapabilities, opts ...Option) *Handler {
	h := &Handler{
		srv:          srv,
		r:            os.Stdin,
		w:            os.Stdout,
		l:            slog.Default(),
		userProvider: OSUserProvider{},
	}
	for _, opt := range opts {
		opt(h)
	}
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
	// IO setup
	reader := bufio.NewReader(h.r)
	bw := bufio.NewWriter(h.w)
	wm := &writeMux{w: bw}
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

			// Construct a minimal session for capability discovery and subsequent requests.
			sess = &stdioSession{userID: uid, proto: negotiated}

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
			// notifications/initialized marks that the client completed its side of init.
			// Only after this point should the server begin emitting list_changed notifications.
			if any.Method == string(mcp.InitializedNotificationMethod) && !registered {
				// Register listChanged callbacks where supported. Use Serve context for teardown.
				if resCap, ok, err := h.srv.GetResourcesCapability(ctx, sess); err == nil && ok && resCap != nil {
					if lcap, ok, err := resCap.GetListChangedCapability(ctx, sess); err == nil && ok && lcap != nil {
						_, _ = lcap.Register(ctx, sess, func(_ context.Context, _ sessions.Session, _ string) {
							n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ResourcesListChangedNotificationMethod)}
							_ = wm.writeJSONRPC(n)
						})
					}
				}
				if tcap, ok, err := h.srv.GetToolsCapability(ctx, sess); err == nil && ok && tcap != nil {
					if lcap, ok, err := tcap.GetListChangedCapability(ctx, sess); err == nil && ok && lcap != nil {
						_, _ = lcap.Register(ctx, sess, func(_ context.Context, _ sessions.Session) {
							n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ToolsListChangedNotificationMethod)}
							_ = wm.writeJSONRPC(n)
						})
					}
				}
				if pcap, ok, err := h.srv.GetPromptsCapability(ctx, sess); err == nil && ok && pcap != nil {
					if lcap, ok, err := pcap.GetListChangedCapability(ctx, sess); err == nil && ok && lcap != nil {
						_, _ = lcap.Register(ctx, sess, func(_ context.Context, _ sessions.Session) {
							n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.PromptsListChangedNotificationMethod)}
							_ = wm.writeJSONRPC(n)
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
			// Dispatch
			resp, err := h.handleRequest(ctx, sess, req)
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
	}
	return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "method not found", nil), nil
}

// stdioSession is a minimal sessions.Session implementation for stdio transport.
type stdioSession struct {
	userID string
	proto  string
}

func (s *stdioSession) SessionID() string                                          { return fmt.Sprintf("%d", os.Getpid()) }
func (s *stdioSession) UserID() string                                             { return s.userID }
func (s *stdioSession) ProtocolVersion() string                                    { return s.proto }
func (s *stdioSession) GetSamplingCapability() (sessions.SamplingCapability, bool) { return nil, false }
func (s *stdioSession) GetRootsCapability() (sessions.RootsCapability, bool)       { return nil, false }
func (s *stdioSession) GetElicitationCapability() (sessions.ElicitationCapability, bool) {
	return nil, false
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
