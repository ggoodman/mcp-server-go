package sessions

import (
	"context"
	"errors"
	"time"
)

// ErrRevocationUnsupported is returned by a host that doesn't implement
// revocation semantics. Callers may treat it as "no-op supported" when optional.
var ErrRevocationUnsupported = errors.New("session revocation unsupported by host")

// RevocationScope defines the granularity at which epoch-based invalidation
// applies. Zero values mean the dimension is not used for the scope key.
// Typical default is per-user, optionally refined by ClientID/HostID.
type RevocationScope struct {
	// UserID groups sessions belonging to a user.
	UserID string
	// ClientID optionally targets sessions created by a logical client/host/app.
	ClientID string
	// TenantID optionally scopes within a multi-tenant deployment.
	TenantID string
}

// --- Per-request rendezvous (distributed await/fulfill) ---

// Awaiter provides a one-shot receive for a specific (sessionID, correlationID)
// tuple that represents the outcome of a single in-flight request. Only one
// awaiter may be registered per key at a time.
//
// Semantics:
//   - Recv blocks until the request is fulfilled, canceled, or the context ends.
//   - Cancel makes any current or future Recv return ErrAwaitCanceled.
//   - Implementations MUST ensure BeginAwait happens-before a corresponding send
//     of the outbound request, so that a later Fulfill cannot race ahead.
type Awaiter interface {
	Recv(ctx context.Context) ([]byte, error)
	Cancel(ctx context.Context) error
}

var (
	// ErrAwaitExists indicates there is already a waiter for the key.
	ErrAwaitExists = errors.New("await already registered")
	// ErrAwaitCanceled is returned from Recv when the await was canceled or the
	// session cleaned up.
	ErrAwaitCanceled = errors.New("await canceled")
)

// SessionHost is the minimal contract the sessions package needs the host to
// provide. It combines per-session ordered messaging with minimal revocation
// primitives and works across in-memory and distributed implementations.
type SessionHost interface {
	// Messaging — ordered per session ID with resume via lastEventID.
	PublishSession(ctx context.Context, sessionID string, data []byte) (eventID string, err error)
	SubscribeSession(ctx context.Context, sessionID string, lastEventID string, handler MessageHandlerFunction) error
	CleanupSession(ctx context.Context, sessionID string) error

	// Revocation — optional; return ErrRevocationUnsupported if unimplemented.
	AddRevocation(ctx context.Context, sessionID string, ttl time.Duration) error
	IsRevoked(ctx context.Context, sessionID string) (bool, error)
	BumpEpoch(ctx context.Context, scope RevocationScope) (newEpoch int64, err error)
	GetEpoch(ctx context.Context, scope RevocationScope) (epoch int64, err error)

	// Rendezvous — required; single-consumer, drop-if-nobody-cares delivery.
	// BeginAwait registers a waiter for a specific correlationID under the
	// session, with a TTL for automatic cleanup. Exactly one waiter may exist
	// for a given key. Must be visible to other instances before returning.
	BeginAwait(ctx context.Context, sessionID, correlationID string, ttl time.Duration) (Awaiter, error)
	// Fulfill delivers a response to a registered waiter, returning true if the
	// waiter received it. If there is no waiter (expired/canceled/not created),
	// return false without error (drop is acceptable).
	Fulfill(ctx context.Context, sessionID, correlationID string, data []byte) (delivered bool, err error)
}
