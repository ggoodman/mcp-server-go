package auth

import (
	"context"
	"errors"
	"time"

	"github.com/ggoodman/mcp-server-go/internal/jwtauth"
)

// AccessTokenAuthOption configures optional aspects of the RFC 9068 access
// token authenticator (scopes, algorithms, leeway, etc.). Audience is now a
// required formal argument to NewFromDiscovery instead of an option.
type AccessTokenAuthOption func(*jwtauth.Config)

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
//   - issuer:   authorization server issuer URL
//   - audience: expected audience ("aud") claim – typically your public MCP endpoint URL
//
// Remaining validation knobs (scopes, algs, leeway) are configured via functional options.
func NewFromDiscovery(ctx context.Context, issuer string, audience string, opts ...AccessTokenAuthOption) (SecurityProvider, error) {
	cfg := jwtauth.DefaultConfig()
	cfg.Issuer = issuer
	cfg.ExpectedAudience = audience
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.ExpectedAudience == "" {
		return nil, errors.New("audience is required")
	}
	internal, err := jwtauth.NewFromDiscovery(ctx, cfg)
	if err != nil {
		return nil, err
	}
	sec := SecurityConfig{
		Issuer:      cfg.Issuer,
		Audiences:   []string{cfg.ExpectedAudience},
		AllowedAlgs: append([]string(nil), cfg.AllowedAlgs...),
		Leeway:      cfg.Leeway,
		EnforceExp:  true,
		EnforceNbf:  true,
		Advertise:   true,
	}
	// Populate advertisement-only OIDC metadata if discovery yielded endpoints.
	// Attempt to extract extended discovery metadata via a private interface.
	type fullDiscovery interface {
		AuthorizationEndpoint() string
		TokenEndpoint() string
	}
	if dm, ok := any(internal).(fullDiscovery); ok {
		if dm.AuthorizationEndpoint() != "" || dm.TokenEndpoint() != "" {
			// Initialize OIDCExtra if needed; we only currently expose endpoints here.
			sec.OIDC = &OIDCExtra{AuthorizationEndpoint: dm.AuthorizationEndpoint(), TokenEndpoint: dm.TokenEndpoint()}
		}
	}
	sec.Normalize()
	return &adapter{a: internal, sec: sec}, nil
}

// adapter wraps the internal authenticator to satisfy the public interface.
type adapter struct {
	a   jwtauth.Authenticator
	sec SecurityConfig
}

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

func (ad *adapter) SecurityConfig() SecurityConfig { return ad.sec.Copy() }

type userInfoAdapter struct{ ui jwtauth.UserInfo }

func (u userInfoAdapter) UserID() string       { return u.ui.UserID() }
func (u userInfoAdapter) Claims(ref any) error { return u.ui.Claims(ref) }
