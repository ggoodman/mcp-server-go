package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	osuser "os/user"
	"strings"

	"github.com/ggoodman/mcp-server-go/internal/engine"
	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/internal/logctx"
	"github.com/ggoodman/mcp-server-go/internal/sessioncore"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
)

type Handler struct {
	log *slog.Logger
	r   io.Reader
	w   io.Writer
	eng *engine.Engine
	srv mcpservice.ServerCapabilities

	initialized bool
	session     sessions.Session

	host sessions.SessionHost
	mgr  *sessioncore.Manager
}

func NewHandler(srv mcpservice.ServerCapabilities, opts ...Option) *Handler {
	h := &Handler{
		log:  slog.Default(),
		r:    os.Stdin,
		w:    os.Stdout,
		srv:  srv,
		host: memoryhost.New(),
	}
	h.mgr = sessioncore.NewManager(h.host)
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *Handler) Serve(ctx context.Context) error {
	logger := h.log.With(slog.String("component", "stdio.Handler"))
	scanner := bufio.NewScanner(h.r)
	sessID := fmt.Sprintf("pid-%d", os.Getpid())
	userID := fmt.Sprintf("user-%d", os.Getuid())

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			return nil
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			h.writeError(logger, nil, jsonrpc.ErrorCodeInvalidRequest, "batch requests not supported", nil)
			continue
		}
		var first any
		if err := json.Unmarshal([]byte(line), &first); err != nil {
			h.writeError(logger, nil, jsonrpc.ErrorCodeParseError, "parse error", nil)
			continue
		}
		if _, ok := first.(map[string]any); !ok {
			h.writeError(logger, nil, jsonrpc.ErrorCodeInvalidRequest, "invalid request", nil)
			continue
		}
		var msg jsonrpc.AnyMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			h.writeError(logger, nil, jsonrpc.ErrorCodeInvalidRequest, "invalid request", err.Error())
			continue
		}
		switch msg.Type() {
		case "request":
			req := msg.AsRequest()

			if req == nil {
				h.writeError(logger, nil, jsonrpc.ErrorCodeInvalidRequest, "invalid request", nil)
				continue
			}

			if req.Method == string(mcp.InitializeMethod) {
				if err := h.doInitialize(ctx, logger, req); err != nil {
					return err
				}
				continue
			}
			if !h.initialized {
				h.writeError(logger, req.ID, jsonrpc.ErrorCodeInvalidRequest, "protocol not initialized", nil)
				continue
			}

			// Handle the request asynchronously to allow concurrent processing
			go func() {
				res, err := h.eng.HandleRequest(ctx, sessID, userID, req)
				if err != nil {
					h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "internal error", err.Error())
					return
				}

				if err := h.writeJSON(res); err != nil {
					logger.Error("write response error", slog.String("err", err.Error()))
				}
			}()
		case "notification":
			req := msg.AsRequest()

			if req == nil {
				h.writeError(logger, nil, jsonrpc.ErrorCodeInvalidRequest, "invalid request", nil)
				continue
			}

			if req.Method == string(mcp.InitializedNotificationMethod) {
				if h.initialized {
					h.writeError(logger, nil, jsonrpc.ErrorCodeInvalidRequest, "already initialized", nil)
					continue
				}

				h.initialized = true
				continue
			}

			if !h.initialized {
				h.writeError(logger, nil, jsonrpc.ErrorCodeInvalidRequest, "protocol not initialized", nil)
				continue
			}

			// Handle the notification asynchronously to allow concurrent processing
			go func() {
				err := h.eng.HandleNotification(ctx, sessID, userID, req)
				if err != nil {
					h.log.Error("notification handler error", slog.String("method", req.Method), slog.String("err", err.Error()))
				}
			}()
			if err := h.handleNotification(ctx, h.session, msg.AsRequest()); err != nil {
				logger.Error("notification handler error", slog.String("method", msg.Method), slog.String("err", err.Error()))
			}
		case "response":
			if !h.initialized {
				h.writeError(logger, msg.ID, jsonrpc.ErrorCodeInvalidRequest, "protocol not initialized", nil)
				continue
			}
			if err := h.handleResponse(ctx, h.session, msg.AsResponse()); err != nil {
				logger.Error("response handler error", slog.String("err", err.Error()))
			}
		default:
			h.writeError(logger, msg.ID, jsonrpc.ErrorCodeInvalidRequest, "unknown message type", nil)
		}
	}
}

func (h *Handler) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := h.w.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func (h *Handler) writeError(logger *slog.Logger, id *jsonrpc.RequestID, code jsonrpc.ErrorCode, msg string, data any) {
	resp := jsonrpc.NewErrorResponse(id, code, msg, data)
	if err := h.writeJSON(resp); err != nil {
		logger.Error("write error", slog.String("err", err.Error()))
	}
}

func (h *Handler) doInitialize(ctx context.Context, logger *slog.Logger, req *jsonrpc.Request) error { //nolint:funlen
	if h.initialized {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInvalidRequest, "already initialized", nil)
		return nil
	}
	if req.ID == nil || req.ID.IsNil() {
		h.writeError(logger, nil, jsonrpc.ErrorCodeInvalidRequest, "initialize must include id", nil)
		return nil
	}
	var initReq mcp.InitializeRequest
	if len(req.Params) == 0 {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInvalidParams, "missing params", nil)
		return nil
	}
	if err := json.Unmarshal(req.Params, &initReq); err != nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid initialize params", err.Error())
		return nil
	}
	negotiated := initReq.ProtocolVersion
	if v, ok, err := h.srv.GetPreferredProtocolVersion(ctx); err != nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to get preferred protocol version", err.Error())
		return nil
	} else if ok && v != "" {
		negotiated = v
	}
	capSet := sessions.CapabilitySet{}
	if initReq.Capabilities.Sampling != nil {
		capSet.Sampling = true
	}
	if initReq.Capabilities.Roots != nil {
		capSet.Roots = true
		capSet.RootsListChanged = initReq.Capabilities.Roots.ListChanged
	}
	if initReq.Capabilities.Elicitation != nil {
		capSet.Elicitation = true
	}
	uid := currentUserID()
	if uid == "" {
		uid = "local"
	}
	session, err := h.mgr.CreateSession(ctx, uid, negotiated, capSet, sessions.MetadataClientInfo{Name: initReq.ClientInfo.Name, Version: initReq.ClientInfo.Version})
	if err != nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to create session", err.Error())
		return nil
	}
	h.session = session
	serverInfo, err := h.srv.GetServerInfo(ctx, session)
	if err != nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to get server info", err.Error())
		return nil
	}
	initRes := &mcp.InitializeResult{ProtocolVersion: negotiated, ServerInfo: serverInfo}
	if instr, ok, err := h.srv.GetInstructions(ctx, session); err != nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to get instructions", err.Error())
		return nil
	} else if ok {
		initRes.Instructions = instr
	}
	if resCap, ok, err := h.srv.GetResourcesCapability(ctx, session); err != nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to get resources capability", err.Error())
		return nil
	} else if ok {
		subCap, hasSub, err := resCap.GetSubscriptionCapability(ctx, session)
		if err != nil {
			h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to get resources subscription capability", err.Error())
			return nil
		}
		_ = subCap
		listChangedCap, hasLC, err := resCap.GetListChangedCapability(ctx, session)
		if err != nil {
			h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to get resources listChanged capability", err.Error())
			return nil
		}
		_ = listChangedCap
		initRes.Capabilities.Resources = &struct {
			ListChanged bool `json:"listChanged"`
			Subscribe   bool `json:"subscribe"`
		}{ListChanged: hasLC, Subscribe: hasSub}
	}
	if toolsCap, ok, err := h.srv.GetToolsCapability(ctx, session); err != nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to get tools capability", err.Error())
		return nil
	} else if ok {
		lc, hasLC, err := toolsCap.GetListChangedCapability(ctx, session)
		if err != nil {
			h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to get tools listChanged capability", err.Error())
			return nil
		}
		_ = lc
		initRes.Capabilities.Tools = &struct {
			ListChanged bool `json:"listChanged"`
		}{ListChanged: hasLC}
	}
	if promptsCap, ok, err := h.srv.GetPromptsCapability(ctx, session); err != nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to get prompts capability", err.Error())
		return nil
	} else if ok {
		lc, hasLC, err := promptsCap.GetListChangedCapability(ctx, session)
		if err != nil {
			h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to get prompts listChanged capability", err.Error())
			return nil
		}
		_ = lc
		initRes.Capabilities.Prompts = &struct {
			ListChanged bool `json:"listChanged"`
		}{ListChanged: hasLC}
	}
	if _, ok, err := h.srv.GetLoggingCapability(ctx, session); err != nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to get logging capability", err.Error())
		return nil
	} else if ok {
		initRes.Capabilities.Logging = &struct{}{}
	}
	if _, ok, err := h.srv.GetCompletionsCapability(ctx, session); err != nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to get completions capability", err.Error())
		return nil
	} else if ok {
		initRes.Capabilities.Completions = &struct{}{}
	}
	resp, err := jsonrpc.NewResultResponse(req.ID, initRes)
	if err != nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "failed to marshal initialize result", err.Error())
		return nil
	}
	if err := h.writeJSON(resp); err != nil {
		return err
	}
	h.initialized = true
	if err := h.writeJSON(&jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.InitializedNotificationMethod)}); err != nil {
		return err
	}
	logger.Info("initialized", slog.String("protocol", negotiated), slog.String("client", fmt.Sprintf("%s/%s", initReq.ClientInfo.Name, initReq.ClientInfo.Version)), slog.String("session_id", session.SessionID()))
	return nil
}

func (h *Handler) handleRequest(ctx context.Context, session sessions.Session, req *jsonrpc.Request) error {
	internalErr := func(id *jsonrpc.RequestID) *jsonrpc.Response {
		return jsonrpc.NewErrorResponse(id, jsonrpc.ErrorCodeInternalError, "internal error", nil)
	}

	switch req.Method {
	case string(mcp.PingMethod):
		return jsonrpc.NewResultResponse(req.ID, struct{}{})
	case string(mcp.ToolsListMethod):
		toolsCap, ok, err := h.mcp.GetToolsCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || toolsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "tools capability not supported", nil), nil
		}

		var listToolsReq mcp.ListToolsRequest
		if err := json.Unmarshal(req.Params, &listToolsReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		page, err := toolsCap.ListTools(ctx, session, func() *string {
			if listToolsReq.Cursor == "" {
				return nil
			}
			s := listToolsReq.Cursor
			return &s
		}())
		if err != nil {
			return internalErr(req.ID), nil
		}

		return jsonrpc.NewResultResponse(req.ID, &mcp.ListToolsResult{
			Tools: page.Items,
			PaginatedResult: mcp.PaginatedResult{
				NextCursor: func() string {
					if page.NextCursor == nil {
						return ""
					}
					return *page.NextCursor
				}(),
			},
		})
	}

	writeError(req.ID, jsonrpc.ErrorCodeMethodNotFound, "method not implemented", nil)
	return nil
}

func (h *Handler) handleNotification(ctx context.Context, session sessions.Session, req *jsonrpc.Request) error {
	logger := logctx.Enrich(ctx, h.log).With(
		slog.String("transport", "streaminghttp"),
		slog.String("op", "notification"),
		slog.String("method", req.Method),
	)
	logger.InfoContext(ctx, "notification.start")

	return nil
}
func (h *Handler) handleResponse(ctx context.Context, session sessions.Session, resp *jsonrpc.Response) error {
	return nil
}

func currentUserID() string {
	if u, err := osuser.Current(); err == nil {
		return u.Uid
	}
	return ""
}
