package sessioncore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/google/uuid"
)

// --- Per-request rendezvous (distributed await/fulfill) ---

// Awaiter provides a one-shot receive for a specific (sessionID, correlationID)
// tuple representing the outcome of a single in-flight request. At most one
// awaiter may exist per key. It is concurrency-safe.
//   - Recv blocks until fulfilled, canceled, or context done.
//   - Cancel causes current/future Recv to return ErrAwaitCanceled.
//   - Implementations must ensure BeginAwait happens-before outbound send.
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

// SessionManager creates, loads, and deletes MCP sessions, optionally issuing
// signed session IDs and tracking revocation epochs.
type SessionManager struct {
	backend       sessions.SessionHost
	jws           JWSSignerVerifier // optional; if nil, fall back to opaque UUID session IDs
	revocationTTL time.Duration     // TTL for precise per-session revocation entries
	issuer        string            // optional issuer binding for signed session IDs
	logger        *slog.Logger
}

// ManagerOption configures optional behavior of the session manager.
type ManagerOption func(*SessionManager)

// WithJWSSigner enables JOSE/JWS-signed session IDs with rotating named keys.
func WithJWSSigner(j JWSSignerVerifier) ManagerOption {
	return func(sm *SessionManager) { sm.jws = j }
}

// WithRevocationTTL sets the TTL used for precise per-session revocation entries.
func WithRevocationTTL(d time.Duration) ManagerOption {
	return func(sm *SessionManager) { sm.revocationTTL = d }
}

// WithIssuer enforces that signed session IDs are bound to this issuer value.
func WithIssuer(issuer string) ManagerOption {
	return func(sm *SessionManager) { sm.issuer = issuer }
}

// NewManager constructs a SessionManager with sensible defaults.
func NewManager(backend sessions.SessionHost, opts ...ManagerOption) *SessionManager {
	sm := &SessionManager{
		backend:       backend,
		revocationTTL: 24 * time.Hour,
		logger:        slog.Default(),
	}
	for _, o := range opts {
		o(sm)
	}
	return sm
}

// SessionOption is a functional option for configuring a session created
// by the SessionManager. These options can be used to enable or disable
// specific capabilities for the session.
// SessionOption mutates a SessionHandle during creation.
type SessionOption func(*SessionHandle)

// WithSamplingCapability enables sampling capability on the session.
func WithSamplingCapability() SessionOption {
	return func(s *SessionHandle) {
		s.sampling = &samplingCapabilityImpl{sess: s}
	}
}

// WithRootsCapability enables roots capability; optionally supports listChanged notifications.
func WithRootsCapability(supportsListChanged bool) SessionOption {
	return func(s *SessionHandle) {
		s.roots = &rootsCapabilityImpl{sess: s, supportsListChanged: supportsListChanged}
	}
}

// WithElicitationCapability enables the elicitation capability.
func WithElicitationCapability() SessionOption {
	return func(s *SessionHandle) {
		s.elicitation = &elicitationCapabilityImpl{sess: s}
	}
}

// WithProtocolVersion stores the negotiated protocol version on the session handle.
// WithProtocolVersion stores the negotiated protocol version on the session handle.
func WithProtocolVersion(version string) SessionOption {
	return func(s *SessionHandle) {
		s.protocolVersion = version
	}
}

// CreateSession allocates a new session for the given user.
func (sm *SessionManager) CreateSession(ctx context.Context, userID string, opts ...SessionOption) (*SessionHandle, error) {
	// Snapshot current epoch if available for embed
	var epoch int64
	if sm.backend != nil {
		if e, err := sm.backend.GetEpoch(ctx, sessions.RevocationScope{UserID: userID}); err == nil {
			epoch = e
		}
	}

	// Build handle and apply options (so proto can be baked into token if signer is enabled)
	session := &SessionHandle{userID: userID, backend: sm.backend}
	for _, opt := range opts {
		opt(session)
	}

	sid := sm.mintSessionID(session, epoch)
	session.id = sid
	return session, nil
}

// LoadSession validates and parses a supplied session ID, returning a handle.
func (sm *SessionManager) LoadSession(ctx context.Context, sessID string, userID string) (*SessionHandle, error) {
	// If the caller canceled the context, treat this as a failure immediately.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Parse and (if applicable) verify a signed session token
	tok, isSigned, err := sm.parseSessionID(sessID)
	if err != nil {
		return nil, fmt.Errorf("invalid session id: %w", err)
	}
	if isSigned {
		if tok.UserID != userID {
			return nil, fmt.Errorf("invalid session: user mismatch")
		}
		if sm.issuer != "" && tok.Issuer != sm.issuer {
			return nil, fmt.Errorf("invalid session: issuer mismatch")
		}
		// Epoch check if host supports it
		if sm.backend != nil {
			if curr, err := sm.backend.GetEpoch(ctx, sessions.RevocationScope{UserID: userID}); err == nil {
				if tok.Epoch < curr {
					return nil, fmt.Errorf("invalid session: revoked by epoch")
				}
			}
		}
	}

	// Precise revocation check if supported
	if sm.backend != nil {
		if revoked, err := sm.backend.IsRevoked(ctx, sessID); err == nil && revoked {
			return nil, fmt.Errorf("invalid session: revoked")
		}
	}

	// Construct the live session wrapper
	s := &SessionHandle{id: sessID, userID: userID, backend: sm.backend}
	if isSigned {
		s.protocolVersion = tok.Proto
		// Rehydrate client capability flags so other nodes observe the same surface.
		if tok.Caps != nil {
			if tok.Caps.Sampling {
				s.sampling = &samplingCapabilityImpl{sess: s}
			}
			if tok.Caps.Roots {
				s.roots = &rootsCapabilityImpl{sess: s, supportsListChanged: tok.Caps.RootsLC}
			}
			if tok.Caps.Elicitation {
				s.elicitation = &elicitationCapabilityImpl{sess: s}
			}
		}
	}
	return s, nil
}

func (sm *SessionManager) DeleteSession(ctx context.Context, sessID string) error {
	// Best-effort precise revocation with bounded TTL
	if sm.backend != nil {
		if err := sm.backend.AddRevocation(ctx, sessID, sm.revocationTTL); err != nil && err != sessions.ErrRevocationUnsupported {
			return fmt.Errorf("failed to revoke session: %w", err)
		}
	}

	// Optional: bump user epoch if the token is signed and reveals the user.
	if tok, isSigned, err := sm.parseSessionID(sessID); err == nil && isSigned && sm.backend != nil {
		if _, err := sm.backend.BumpEpoch(ctx, sessions.RevocationScope{UserID: tok.UserID}); err != nil && err != sessions.ErrRevocationUnsupported {
			sm.logger.Warn("failed to bump epoch during session delete",
				slog.String("session_id", sessID),
				slog.String("user_id", tok.UserID),
				slog.String("error", err.Error()),
			)
			// Log and continue; this is non-fatal
			// Non-fatal; continue cleanup even if epoch bump fails
		}
	}

	// Cleanup delivery resources for this session
	if sm.backend != nil {
		if err := sm.backend.CleanupSession(ctx, sessID); err != nil {
			return fmt.Errorf("failed to cleanup session: %w", err)
		}
	}
	return nil
}

// --- Token minting/parsing helpers (JOSE/JWS backed) ---

// sessionTokenV1 is a compact, signed, stateless description of a session handle.
// This is intentionally minimal and signed as a compact JWS. Not a full JWT with exp.
type sessionTokenV1 struct {
	V      int     `json:"v"` // version = 1
	UserID string  `json:"uid"`
	JTI    string  `json:"jti"` // random id
	IAT    int64   `json:"iat"` // unix seconds
	Epoch  int64   `json:"gen,omitempty"`
	Issuer string  `json:"iss,omitempty"`
	Proto  string  `json:"proto,omitempty"`
	Caps   *capsV1 `json:"caps,omitempty"`
}

// capsV1 is a compact summary of the client's declared capabilities
// that were enabled for this session. It allows other nodes to
// reconstruct the same surface without re-running initialize logic.
type capsV1 struct {
	Roots       bool `json:"roots,omitempty"`
	RootsLC     bool `json:"rootsLC,omitempty"`
	Sampling    bool `json:"sampling,omitempty"`
	Elicitation bool `json:"elicitation,omitempty"`
}

func (sm *SessionManager) mintSessionID(s *SessionHandle, epoch int64) string {
	// If no JWS signer is configured, fall back to an opaque UUID session id.
	if sm.jws == nil {
		return uuid.NewString()
	}
	// Summarize enabled client capabilities from the handle
	var caps *capsV1
	if s != nil {
		c := capsV1{}
		if s.roots != nil {
			c.Roots = true
			if rc, ok := s.roots.(*rootsCapabilityImpl); ok && rc.supportsListChanged {
				c.RootsLC = true
			}
		}
		if s.sampling != nil {
			c.Sampling = true
		}
		if s.elicitation != nil {
			c.Elicitation = true
		}
		if c.Roots || c.RootsLC || c.Sampling || c.Elicitation {
			caps = &c
		}
	}

	tok := sessionTokenV1{V: 1, UserID: s.userID, JTI: uuid.NewString(), IAT: time.Now().Unix(), Epoch: epoch, Issuer: sm.issuer, Proto: s.protocolVersion, Caps: caps}
	payload, _ := json.Marshal(tok)
	compact, err := sm.jws.Sign(payload)
	if err != nil {
		// If signing fails, avoid creating an unusable session; fall back to UUID.
		return uuid.NewString()
	}
	return compact
}

func (sm *SessionManager) parseSessionID(sessID string) (sessionTokenV1, bool, error) {
	var zero sessionTokenV1
	if sm.jws == nil {
		return zero, false, nil // opaque/legacy id
	}
	payloadB, _, err := sm.jws.Verify(sessID)
	if err != nil {
		return zero, true, fmt.Errorf("verify failed: %w", err)
	}
	var tok sessionTokenV1
	if err := json.Unmarshal(payloadB, &tok); err != nil {
		return zero, true, fmt.Errorf("invalid payload: %w", err)
	}
	if tok.V != 1 {
		return zero, true, fmt.Errorf("unsupported version")
	}
	return tok, true, nil
}
