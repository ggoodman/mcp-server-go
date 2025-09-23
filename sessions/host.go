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

	// --- Server-internal, ordered event fan-out (not delivered to clients directly) ---
	// PublishEvent appends an event to the per-session, per-topic internal event
	// stream used for server-side coordination (never delivered directly to the
	// MCP client). Semantics:
	//   * Ordering: Events are totally ordered per (sessionID, topic) according
	//     to the order PublishEvent is called successfully.
	//   * Visibility: Subscribers only receive events published AFTER they
	//     subscribe (no historical replay obligation).
	//   * Delivery: At-least-once to every active subscriber. Implementations
	//     strive for exactly-once but callers must tolerate duplicates under
	//     rare failure / reconnect scenarios (especially in distributed hosts).
	//   * Fan-out: All current subscribers for (sessionID, topic) get every
	//     subsequent event (subject to at-least-once guarantee).
	//   * Retention: Minimal retention is sufficient; implementations may prune
	//     or discard already-delivered events as long as future delivery
	//     guarantees hold. No replay API is exposed.
	//   * Lifetime: Events need not survive process restarts; durability is best
	//     effort unless an implementation chooses otherwise. Deleting a session
	//     MUST remove all associated event streams.
	PublishEvent(ctx context.Context, sessionID, topic string, payload []byte) error
	// SubscribeEvents registers a handler that will receive (in order) each
	// event published after subscription time for the given (sessionID, topic).
	// The passed context governs the lifetime. Returning a non-nil error from the
	// handler ends the subscription with that error. Implementations SHOULD make
	// a best-effort to avoid dropping events, but at-least-once (not exactly-once)
	//  delivery is the contract.
	SubscribeEvents(ctx context.Context, sessionID, topic string, handler EventHandlerFunction) error

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
