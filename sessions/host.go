package sessions

import (
	"context"
)

// SessionHost defines the unified storage + messaging contract for sessions.
// Implementations MUST be safe for concurrent use and suitable for horizontal
// scaling. All methods should treat unknown session IDs as no-ops or return a
// sentinel error (implementation-specific) rather than panicking.
type SessionHost interface {
	// --- Ordered client-facing messaging stream ---
	PublishSession(ctx context.Context, sessionID string, data []byte) (eventID string, err error)
	SubscribeSession(ctx context.Context, sessionID string, lastEventID string, handler MessageHandlerFunction) error

	// --- Server-internal event fan-out (not delivered to clients directly) ---
	PublishEvent(ctx context.Context, sessionID, topic string, payload []byte) error
	SubscribeEvents(ctx context.Context, sessionID, topic string, handler EventHandlerFunction) (unsubscribe func(), err error)

	// --- Session metadata lifecycle ---
	CreateSession(ctx context.Context, meta *SessionMetadata) error
	GetSession(ctx context.Context, sessionID string) (*SessionMetadata, error)
	MutateSession(ctx context.Context, sessionID string, fn func(*SessionMetadata) error) error
	TouchSession(ctx context.Context, sessionID string) error
	DeleteSession(ctx context.Context, sessionID string) error // idempotent hard delete

	// --- Per-session bounded KV storage ---
	PutSessionData(ctx context.Context, sessionID, key string, value []byte) error
	GetSessionData(ctx context.Context, sessionID, key string) ([]byte, error)
	DeleteSessionData(ctx context.Context, sessionID, key string) error
}

// EventHandlerFunction handles server-internal events for a specific topic.
// Returning an error will terminate the subscription with that error.
type EventHandlerFunction func(ctx context.Context, payload []byte) error
