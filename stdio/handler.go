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

	"github.com/ggoodman/mcp-server-go/internal/engine"
	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
)

type Handler struct {
	log    *slog.Logger
	r      io.Reader
	w      io.Writer
	engine *engine.Engine
	host   sessions.SessionHost

	initialized bool
	session     sessions.Session
}

func NewHandler(srv mcpservice.ServerCapabilities, opts ...Option) *Handler {
	h := &Handler{
		log:  slog.Default(),
		r:    os.Stdin,
		w:    os.Stdout,
		host: memoryhost.New(),
	}
	h.engine = engine.NewEngine(h.host, srv, engine.WithLogger(h.log))
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
		var msg jsonrpc.AnyMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			h.writeError(logger, nil, jsonrpc.ErrorCodeInvalidRequest, "invalid request", err.Error())
			continue
		}
		switch msg.Type() {
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
