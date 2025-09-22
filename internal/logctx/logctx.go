package logctx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
)

// context key type
type ctxKey int

const (
	requestIDKey ctxKey = iota
	sessionIDKey
	userIDKey
)

func WithRequestID(ctx context.Context, rid string) context.Context {
	if rid == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey, rid)
}
func WithSessionID(ctx context.Context, sid string) context.Context {
	if sid == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDKey, sid)
}
func WithUserID(ctx context.Context, uid string) context.Context {
	if uid == "" {
		return ctx
	}
	return context.WithValue(ctx, userIDKey, uid)
}
func RequestID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(requestIDKey).(string)
	return v, ok && v != ""
}
func SessionID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(sessionIDKey).(string)
	return v, ok && v != ""
}
func UserID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(userIDKey).(string)
	return v, ok && v != ""
}

func Enrich(ctx context.Context, l *slog.Logger) *slog.Logger {
	if l == nil {
		l = slog.Default()
	}
	args := []any{}
	if rid, ok := RequestID(ctx); ok {
		args = append(args, slog.String("request_id", rid))
	}
	if sid, ok := SessionID(ctx); ok {
		args = append(args, slog.String("session_id", sid))
	}
	if uid, ok := UserID(ctx); ok {
		args = append(args, slog.String("user_id", uid))
	}
	if len(args) == 0 {
		return l
	}
	return l.With(args...)
}

func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
