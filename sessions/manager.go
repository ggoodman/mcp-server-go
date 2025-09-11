package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type SessionManager interface {
	CreateSession(ctx context.Context, userID string, opts ...SessionOption) (Session, error)

	LoadSession(ctx context.Context, sessID string, userID string) (Session, error)

	DeleteSession(ctx context.Context, sessID string) error
}

var _ SessionManager = (*sessionManager)(nil)

type sessionManager struct {
	backend       SessionHost
	jws           JWSSignerVerifier // optional; if nil, fall back to opaque UUID session IDs
	revocationTTL time.Duration     // TTL for precise per-session revocation entries
	issuer        string            // optional issuer binding for signed session IDs
}

// ManagerOption configures optional behavior of the session manager.
type ManagerOption func(*sessionManager)

// WithJWSSigner enables JOSE/JWS-signed session IDs with rotating named keys.
func WithJWSSigner(j JWSSignerVerifier) ManagerOption {
	return func(sm *sessionManager) { sm.jws = j }
}

// WithRevocationTTL sets the TTL used for precise per-session revocation entries.
func WithRevocationTTL(d time.Duration) ManagerOption {
	return func(sm *sessionManager) { sm.revocationTTL = d }
}

// WithIssuer enforces that signed session IDs are bound to this issuer value.
func WithIssuer(issuer string) ManagerOption {
	return func(sm *sessionManager) { sm.issuer = issuer }
}

func NewManager(backend SessionHost, opts ...ManagerOption) *sessionManager {
	sm := &sessionManager{
		backend:       backend,
		revocationTTL: 24 * time.Hour,
	}
	for _, o := range opts {
		o(sm)
	}
	return sm
}

func (sm *sessionManager) CreateSession(ctx context.Context, userID string, opts ...SessionOption) (Session, error) {
	// Snapshot current epoch if available for embed
	var epoch int64
	if sm.backend != nil {
		if e, err := sm.backend.GetEpoch(ctx, RevocationScope{UserID: userID}); err == nil {
			epoch = e
		}
	}

	sid := sm.mintSessionID(userID, epoch)

	session := &session{id: sid, userID: userID, backend: sm.backend}

	for _, opt := range opts {
		opt(session)
	}

	return session, nil
}

func (sm *sessionManager) LoadSession(ctx context.Context, sessID string, userID string) (Session, error) {
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
			if curr, err := sm.backend.GetEpoch(ctx, RevocationScope{UserID: userID}); err == nil {
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
	s := &session{id: sessID, userID: userID, backend: sm.backend}
	return s, nil
}

func (sm *sessionManager) DeleteSession(ctx context.Context, sessID string) error {
	// Best-effort precise revocation with bounded TTL
	if sm.backend != nil {
		if err := sm.backend.AddRevocation(ctx, sessID, sm.revocationTTL); err != nil && err != ErrRevocationUnsupported {
			return fmt.Errorf("failed to revoke session: %w", err)
		}
	}

	// Optional: bump user epoch if the token is signed and reveals the user.
	if tok, isSigned, err := sm.parseSessionID(sessID); err == nil && isSigned && sm.backend != nil {
		if _, err := sm.backend.BumpEpoch(ctx, RevocationScope{UserID: tok.UserID}); err != nil && err != ErrRevocationUnsupported {
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
	V      int    `json:"v"` // version = 1
	UserID string `json:"uid"`
	JTI    string `json:"jti"` // random id
	IAT    int64  `json:"iat"` // unix seconds
	Epoch  int64  `json:"gen,omitempty"`
	Issuer string `json:"iss,omitempty"`
}

func (sm *sessionManager) mintSessionID(userID string, epoch int64) string {
	// If no JWS signer is configured, fall back to an opaque UUID session id.
	if sm.jws == nil {
		return uuid.NewString()
	}
	tok := sessionTokenV1{V: 1, UserID: userID, JTI: uuid.NewString(), IAT: time.Now().Unix(), Epoch: epoch, Issuer: sm.issuer}
	payload, _ := json.Marshal(tok)
	compact, err := sm.jws.Sign(payload)
	if err != nil {
		// If signing fails, avoid creating an unusable session; fall back to UUID.
		return uuid.NewString()
	}
	return compact
}

func (sm *sessionManager) parseSessionID(sessID string) (sessionTokenV1, bool, error) {
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
