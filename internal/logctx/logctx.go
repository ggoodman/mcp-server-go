package logctx

import (
	"context"
	"log/slog"
)

type Handler struct {
	slog.Handler
}

func (h Handler) Handle(ctx context.Context, r slog.Record) error {
	if rd, ok := ctx.Value(requestDataKey{}).(RequestData); ok {
		r.AddAttrs(slog.Group("req",
			slog.String("request_id", rd.RequestID),
			slog.String("method", rd.Method),
			slog.String("user_agent", rd.UserAgent),
			slog.String("remote_addr", rd.RemoteAddr),
			slog.String("path", rd.Path),
		))
	}

	return h.Handler.Handle(ctx, r)
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
