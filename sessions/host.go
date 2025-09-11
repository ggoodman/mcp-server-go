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
}
