package sessions

import "context"

// SessionData is an optional extension for per-session key/value storage.
// Callers should use a type assertion: if ds, ok := sess.(SessionData); ok { ... }.
type SessionData interface {
	PutData(ctx context.Context, key string, value []byte) error
	GetData(ctx context.Context, key string) ([]byte, error)
	DeleteData(ctx context.Context, key string) error
}
