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
	"strings"
	"sync"

	"github.com/ggoodman/mcp-server-go/internal/engine"
	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
)

const sessionFanoutTopic = "session:events"

type Handler struct {
	log    *slog.Logger
	r      io.Reader
	w      io.Writer
	engine *engine.Engine
	host   sessions.SessionHost

	writeMu sync.Mutex
	userID  string

	initialized   bool
	sessionOpen   bool
	session       sessions.Session
	sessionCancel context.CancelFunc
}

func NewHandler(srv mcpservice.ServerCapabilities, opts ...Option) *Handler {
	h := &Handler{
		log:    slog.Default(),
		r:      os.Stdin,
		w:      os.Stdout,
		host:   memoryhost.New(),
		userID: fmt.Sprintf("user-%d", os.Getuid()),
	}
	h.engine = engine.NewEngine(h.host, srv, engine.WithLogger(h.log))
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *Handler) Serve(ctx context.Context) error {
	logger := h.log.With(slog.String("component", "stdio.Handler"))
	engineCtx, cancelEngine := context.WithCancel(ctx)
	defer cancelEngine()

	go func() {
		if err := h.engine.Run(engineCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("engine.run.fail", slog.String("err", err.Error()))
		}
	}()

	scanner := bufio.NewScanner(h.r)
	if h.userID == "" {
		h.userID = fmt.Sprintf("user-%d", os.Getuid())
	}

	for {
		select {
		case <-ctx.Done():
			h.stopSessionPump()
			return ctx.Err()
		default:
		}

		if !scanner.Scan() {
			h.stopSessionPump()
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

		var msg jsonrpc.AnyMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			h.writeError(logger, nil, jsonrpc.ErrorCodeInvalidRequest, "invalid request", err.Error())
			continue
		}

		switch msg.Type() {
		case "request":
			req := msg.AsRequest()
			if req == nil {
				h.writeError(logger, msg.ID, jsonrpc.ErrorCodeInvalidRequest, "invalid request structure", nil)
				continue
			}
			if req.Method == string(mcp.InitializeMethod) {
				// Initialization is a one-time handshake; handle synchronously.
				h.handleInitialize(engineCtx, logger, req)
				continue
			}

			// Handle non-initialize requests concurrently so we can continue
			// reading stdin for cancellations and client responses.
			reqCopy := *req
			go h.handleRequest(engineCtx, logger, &reqCopy)
		case "notification":
			req := msg.AsRequest()
			if req == nil {
				logger.Warn("notification.malformed")
				continue
			}
			h.handleNotification(engineCtx, logger, req)
		case "response":
			res := msg.AsResponse()
			if res == nil {
				logger.Warn("response.malformed")
				continue
			}
			h.handleClientResponse(engineCtx, logger, res)
		default:
			h.writeError(logger, msg.ID, jsonrpc.ErrorCodeInvalidRequest, "unknown message type", nil)
		}
	}
}

func (h *Handler) stopSessionPump() {
	if h.sessionCancel != nil {
		h.sessionCancel()
		h.sessionCancel = nil
	}
}

func (h *Handler) startSessionPump(ctx context.Context, sessionID string) {
	h.stopSessionPump()
	pumpCtx, cancel := context.WithCancel(ctx)
	h.sessionCancel = cancel

	go func() {
		err := h.host.SubscribeSession(pumpCtx, sessionID, "", func(cbCtx context.Context, msgID string, msg []byte) error {
			return h.writeMessageBytes(msg)
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			h.log.Error("session.stream.fail", slog.String("session_id", sessionID), slog.String("err", err.Error()))
		}
	}()
}

func (h *Handler) handleInitialize(ctx context.Context, logger *slog.Logger, req *jsonrpc.Request) {
	if h.initialized {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInvalidRequest, "session already initialized", nil)
		return
	}

	var initReq mcp.InitializeRequest
	if err := json.Unmarshal(req.Params, &initReq); err != nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid params", err.Error())
		return
	}

	sess, initRes, err := h.engine.InitializeSession(ctx, h.userID, &initReq)
	if err != nil {
		logger.Error("initialize.fail", slog.String("err", err.Error()))
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil)
		return
	}

	h.session = sess
	h.initialized = true
	// Do not start the session pump yet; wait for notifications/initialized.

	resp, err := jsonrpc.NewResultResponse(req.ID, initRes)
	if err != nil {
		logger.Error("initialize.encode.fail", slog.String("err", err.Error()))
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil)
		return
	}

	if err := h.writeJSON(resp); err != nil {
		logger.Error("initialize.write.fail", slog.String("err", err.Error()))
	}
}

func (h *Handler) handleRequest(ctx context.Context, logger *slog.Logger, req *jsonrpc.Request) {
	if !h.initialized || h.session == nil {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInvalidRequest, "session not initialized", nil)
		return
	}
	if !h.sessionOpen {
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInvalidRequest, "session not initialized", nil)
		return
	}

	reqCtx := ctx
	if req.ID != nil {
		if rid := req.ID.String(); rid != "" {
			reqCtx = mcpservice.WithProgressReporter(reqCtx, stdioProgressReporter{handler: h, requestID: rid})
		}
	}

	writer := engine.NewMessageWriterFunc(func(ctx context.Context, msg jsonrpc.Message) error {
		return h.writeMessageBytes(msg)
	})

	res, err := h.engine.HandleRequest(reqCtx, h.session.SessionID(), h.session.UserID(), req, writer)
	if err != nil {
		logger.Error("handle.request.fail", slog.String("method", req.Method), slog.String("err", err.Error()))
		h.writeError(logger, req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil)
		return
	}

	if res == nil {
		return
	}

	if err := h.writeJSON(res); err != nil {
		logger.Error("response.write.fail", slog.String("err", err.Error()))
	}
}

func (h *Handler) handleNotification(ctx context.Context, logger *slog.Logger, note *jsonrpc.Request) {
	if !h.initialized || h.session == nil {
		logger.Warn("notification.before_initialize", slog.String("method", note.Method))
		return
	}

	if err := h.engine.HandleNotification(ctx, h.session.SessionID(), h.session.UserID(), note); err != nil {
		logger.Error("notification.handle.fail", slog.String("method", note.Method), slog.String("err", err.Error()))
		return
	}

	if note.Method == string(mcp.InitializedNotificationMethod) && !h.sessionOpen {
		// Mark session open locally and start pump; engine will flip state via fanout.
		h.sessionOpen = true
		h.startSessionPump(ctx, h.session.SessionID())
	}
}

func (h *Handler) handleClientResponse(ctx context.Context, logger *slog.Logger, res *jsonrpc.Response) {
	if !h.initialized || h.session == nil {
		logger.Warn("response.before_initialize")
		return
	}
	if res.ID == nil || res.ID.IsNil() {
		logger.Warn("response.missing_id")
		return
	}

	payload, err := json.Marshal(res)
	if err != nil {
		logger.Error("response.marshal.fail", slog.String("err", err.Error()))
		return
	}

	envelope, err := json.Marshal(struct {
		SessionID string `json:"sess_id"`
		UserID    string `json:"user_id"`
		Msg       []byte `json:"msg"`
	}{
		SessionID: h.session.SessionID(),
		UserID:    h.session.UserID(),
		Msg:       payload,
	})
	if err != nil {
		logger.Error("response.envelope.fail", slog.String("err", err.Error()))
		return
	}

	if err := h.host.PublishEvent(ctx, sessionFanoutTopic, envelope); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("response.publish.fail", slog.String("err", err.Error()))
	}
}

func (h *Handler) writeMessageBytes(msg []byte) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()

	if _, err := h.w.Write(msg); err != nil {
		return err
	}
	if len(msg) == 0 || msg[len(msg)-1] != '\n' {
		if _, err := h.w.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return h.writeMessageBytes(b)
}

func (h *Handler) writeError(logger *slog.Logger, id *jsonrpc.RequestID, code jsonrpc.ErrorCode, msg string, data any) {
	resp := jsonrpc.NewErrorResponse(id, code, msg, data)
	if err := h.writeJSON(resp); err != nil {
		logger.Error("write error", slog.String("err", err.Error()))
	}
}

type stdioProgressReporter struct {
	handler   *Handler
	requestID string
}

func (p stdioProgressReporter) Report(ctx context.Context, progress, total float64) error {
	if p.handler == nil || p.requestID == "" {
		return nil
	}

	params := mcp.ProgressNotificationParams{
		ProgressToken: mcp.ProgressToken(p.requestID),
		Progress:      progress,
	}
	if total > 0 {
		params.Total = total
	}

	note := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ProgressNotificationMethod)}
	encodedParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	note.Params = encodedParams

	msg, err := json.Marshal(note)
	if err != nil {
		return err
	}

	return p.handler.writeMessageBytes(msg)
}
