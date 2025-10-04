package logctx

import (
	"context"
	"log/slog"

	"github.com/ggoodman/mcp-server-go/sessions"
)

type Handler struct {
	slog.Handler
}

func (h Handler) Handle(ctx context.Context, r slog.Record) error {
	if rd, ok := ctx.Value(requestDataKey{}).(*RequestData); ok {
		r.AddAttrs(slog.Group("req",
			slog.String("id", rd.RequestID),
			slog.String("method", rd.Method),
			slog.String("user_agent", rd.UserAgent),
			slog.String("remote_addr", rd.RemoteAddr),
			slog.String("path", rd.Path),
		))
	}

	if sd, ok := ctx.Value(sessionDataKey{}).(*SessionData); ok {
		r.AddAttrs(slog.Group("sess",
			slog.String("id", sd.SessionID),
			slog.String("user_id", sd.UserID),
			slog.String("protocol_version", sd.ProtocolVersion),
			slog.String("state", string(sd.State)),
		))
	}

	if msg, ok := ctx.Value(rpcMsg{}).(*RPCMessage); ok {
		r.AddAttrs(slog.Group("rpc",
			slog.String("method", msg.Method),
			slog.String("id", msg.ID),
			slog.String("type", msg.Type),
		))
	}

	if td, ok := ctx.Value(toolCallDataKey{}).(*ToolCallData); ok {
		r.AddAttrs(slog.Group("tool",
			slog.String("name", td.ToolName),
		))
	}

	return h.Handler.Handle(ctx, r)
}

type rpcMsg struct{}

type RPCMessage struct {
	Method string
	ID     string
	Type   string
}

func WithRPCMessage(ctx context.Context, msg *RPCMessage) context.Context {
	return context.WithValue(ctx, rpcMsg{}, msg)
}

type requestDataKey struct{}

type RequestData struct {
	RequestID  string
	Method     string
	UserAgent  string
	RemoteAddr string
	Path       string
}

func WithRequestData(ctx context.Context, data *RequestData) context.Context {
	return context.WithValue(ctx, requestDataKey{}, data)
}

type sessionDataKey struct{}

type SessionData struct {
	SessionID       string
	UserID          string
	ProtocolVersion string
	State           sessions.SessionState
}

func WithSessionData(ctx context.Context, data *SessionData) context.Context {
	return context.WithValue(ctx, sessionDataKey{}, data)
}

type toolCallDataKey struct{}

type ToolCallData struct {
	ToolName string
}

func WithToolCallData(ctx context.Context, data *ToolCallData) context.Context {
	return context.WithValue(ctx, toolCallDataKey{}, data)
}
