package auth

import (
	"context"
	"errors"
	"time"

	"github.com/ggoodman/mcp-server-go/internal/jwtauth"
)

// AccessTokenAuthOption configures the RFC 9068 access token authenticator.
type AccessTokenAuthOption func(*jwtauth.Config)

// WithExpectedAudience configures the audience ("aud") this resource expects.
// Typically this is your public MCP endpoint URL.
func WithExpectedAudience(resource string) AccessTokenAuthOption {
	return func(c *jwtauth.Config) { c.ExpectedAudience = resource }
}

// WithRequiredScopes requires all of the provided scopes to be present in the
// space-delimited "scope" claim.
func WithRequiredScopes(scopes ...string) AccessTokenAuthOption {
	return func(c *jwtauth.Config) {
		c.RequiredScopes = append([]string(nil), scopes...)
		c.ScopeModeAny = false
	}
}

// WithAnyRequiredScope requires at least one of the provided scopes to be present.
func WithAnyRequiredScope(scopes ...string) AccessTokenAuthOption {
	return func(c *jwtauth.Config) {
		c.RequiredScopes = append([]string(nil), scopes...)
		c.ScopeModeAny = true
	}
}

// WithAllowedAlgs restricts allowed JWS algorithms. "none" is never allowed.
// Defaults to ["RS256"].
func WithAllowedAlgs(algs ...string) AccessTokenAuthOption {
	return func(c *jwtauth.Config) {
		c.AllowedAlgs = append([]string(nil), algs...)
	}
}

// WithLeeway sets clock skew tolerance for time-based claims.
func WithLeeway(d time.Duration) AccessTokenAuthOption {
	return func(c *jwtauth.Config) { c.Leeway = d }
}

// NewFromDiscovery returns an Authenticator that verifies RFC 9068 JWT access
// tokens discovered via OpenID Connect discovery (jwks_uri, issuer, etc.).
//
// Required:
//   - issuer: the authorization server issuer URL.
//   - WithExpectedAudience must be provided (aud check).
func NewFromDiscovery(ctx context.Context, issuer string, opts ...AccessTokenAuthOption) (Authenticator, error) {
	cfg := jwtauth.DefaultConfig()
	cfg.Issuer = issuer
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.ExpectedAudience == "" {
		return nil, errors.New("WithExpectedAudience is required")
	}
	internal, err := jwtauth.NewFromDiscovery(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &adapter{a: internal}, nil
}

// adapter wraps the internal authenticator to satisfy the public interface.
type adapter struct{ a jwtauth.Authenticator }

func (ad *adapter) CheckAuthentication(ctx context.Context, tok string) (UserInfo, error) {
	ui, err := ad.a.CheckAuthentication(ctx, tok)
	if err != nil {
		// Map internal sentinel errors to public errors used by the handler.
		if errors.Is(err, jwtauth.ErrInsufficientScope) {
			return nil, errors.Join(ErrInsufficientScope, err)
		}
		return nil, errors.Join(ErrUnauthorized, err)
	}
	return userInfoAdapter{ui: ui}, nil
}

type userInfoAdapter struct{ ui jwtauth.UserInfo }

func (u userInfoAdapter) UserID() string       { return u.ui.UserID() }
func (u userInfoAdapter) Claims(ref any) error { return u.ui.Claims(ref) }
