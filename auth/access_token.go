package auth

import (
	"context"
	"errors"
	"strings"
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

// WithExtraAudience accepts a single additional audience (invokable multiple
// times) beyond the primary audience passed to NewFromDiscovery. This is
// mainly for local/testing scenarios where the served MCP endpoint URL (and
// thus audience) differs from the production-configured audience at the auth
// server. Use sparingly in production environments.
func WithExtraAudience(aud string) AccessTokenAuthOption {
	return func(c *jwtauth.Config) {
		if aud == "" {
			return
		}
		for _, existing := range c.ExpectedAudiences {
			if existing == aud {
				return
			}
		}
		c.ExpectedAudiences = append(c.ExpectedAudiences, aud)
	}
}

// WithScopeHint configures an optional set of scopes that will be echoed back
// to clients via the WWW-Authenticate "scope" parameter when the transport
// constructs Bearer challenges (for example on insufficient_scope errors).
//
// These hint scopes are advisory only: they do not affect token validation
// (which remains governed by RequiredScopes/ScopeModeAny) but give generic MCP
// clients a concrete scope set to request during authorization. The list is
// copied so callers may reuse their slice safely.
func WithScopeHint(scopes ...string) AccessTokenAuthOption {
	return func(c *jwtauth.Config) {
		// Normalize by trimming empties and storing a defensive copy.
		filtered := scopes[:0]
		for _, s := range scopes {
			if s = strings.TrimSpace(s); s != "" {
				filtered = append(filtered, s)
			}
		}
		c.HintScopes = append([]string(nil), filtered...)
	}
}

// StaticScopes returns a transform function that ignores discovered scopes and
// returns a fixed set of scopes to advertise. Use with WithAdvertisedScopes to
// advertise a specific set of scopes regardless of what the authorization server
// advertises.
//
// Example:
//
//	WithAdvertisedScopes(auth.StaticScopes("mcp:read", "mcp:write"))
func StaticScopes(scopes ...string) func([]string) []string {
	// Make a defensive copy to avoid aliasing issues
	static := append([]string(nil), scopes...)
	return func(discovered []string) []string {
		return static
	}
}

// FilterScopes returns a transform function that filters discovered scopes
// based on a predicate. Use with WithAdvertisedScopes to selectively advertise
// a subset of discovered scopes.
//
// Example:
//
//	WithAdvertisedScopes(auth.FilterScopes(func(s string) bool {
//	    return !strings.HasPrefix(s, "internal:")
//	}))
func FilterScopes(keep func(string) bool) func([]string) []string {
	return func(discovered []string) []string {
		filtered := make([]string, 0, len(discovered))
		for _, s := range discovered {
			if keep(s) {
				filtered = append(filtered, s)
			}
		}
		return filtered
	}
}

// WithAdvertisedScopes configures a transform function that customizes which
// scopes are advertised in the protected resource metadata (scopes_supported).
// The function receives the scopes discovered from the authorization server's
// OIDC metadata and returns the scopes to advertise.
//
// Use StaticScopes(...) to ignore discovery and advertise a fixed set, or
// FilterScopes(...) to selectively include discovered scopes. For full control,
// provide a custom function that can merge, filter, or transform the discovered
// scopes as needed.
//
// If this option is not used, the discovered scopes are advertised as-is.
func WithAdvertisedScopes(transform func(discovered []string) []string) AccessTokenAuthOption {
	return func(c *jwtauth.Config) {
		c.AdvertisedScopesTransform = transform
	}
}

// buildSecurityConfig constructs a SecurityConfig from the jwtauth.Config and
// an internal authenticator that may optionally expose extended discovery
// metadata via the fullDiscovery interface. This is split out primarily to
// make advertised scope behavior directly testable.
func buildSecurityConfig(cfg *jwtauth.Config, internal any) SecurityConfig {
	sec := SecurityConfig{
		Issuer:      cfg.Issuer,
		Audiences:   append([]string(nil), cfg.ExpectedAudiences...),
		AllowedAlgs: append([]string(nil), cfg.AllowedAlgs...),
		Leeway:      cfg.Leeway,
		EnforceExp:  true,
		EnforceNbf:  true,
		Advertise:   true,
		HintScopes:  append([]string(nil), cfg.HintScopes...),
	}

	// Populate advertisement-only OIDC metadata if discovery yielded endpoints.
	// Attempt to extract extended discovery metadata via a private interface.
	type fullDiscovery interface {
		AuthorizationEndpoint() string
		TokenEndpoint() string
		ResponseTypes() []string
		Scopes() []string
		GrantTypes() []string
		ResponseModes() []string
		CodeChallengeMethods() []string
		TokenEndpointAuthMethods() []string
		TokenEndpointAuthAlgs() []string
		ServiceDocumentation() string
		PolicyURI() string
		TosURI() string
		RegistrationEndpoint() string
	}
	if dm, ok := internal.(fullDiscovery); ok {
		// Apply advertised scopes transform if configured
		discoveredScopes := dm.Scopes()
		advertisedScopes := discoveredScopes
		if cfg.AdvertisedScopesTransform != nil {
			advertisedScopes = cfg.AdvertisedScopesTransform(discoveredScopes)
		}
		sec.OIDC = &OIDCExtra{
			AuthorizationEndpoint:                      dm.AuthorizationEndpoint(),
			TokenEndpoint:                              dm.TokenEndpoint(),
			RegistrationEndpoint:                       dm.RegistrationEndpoint(),
			ResponseTypesSupported:                     dm.ResponseTypes(),
			ScopesSupported:                            advertisedScopes,
			GrantTypesSupported:                        dm.GrantTypes(),
			ResponseModesSupported:                     dm.ResponseModes(),
			CodeChallengeMethodsSupported:              dm.CodeChallengeMethods(),
			TokenEndpointAuthMethodsSupported:          dm.TokenEndpointAuthMethods(),
			TokenEndpointAuthSigningAlgValuesSupported: dm.TokenEndpointAuthAlgs(),
			ServiceDocumentation:                       dm.ServiceDocumentation(),
			OpPolicyURI:                                dm.PolicyURI(),
			OpTosURI:                                   dm.TosURI(),
		}
	}

	sec.Normalize()
	return sec
}

// NewFromDiscovery returns an Authenticator that verifies RFC 9068 JWT access
// tokens discovered via OpenID Connect discovery (jwks_uri, issuer, etc.).
//
// Required:
//   - issuer:   authorization server issuer URL
//   - audience: expected audience ("aud") claim – typically your public MCP endpoint URL
//
// Remaining validation knobs (scopes, algs, leeway) are configured via functional options.
//
// For local development where the Authorization Server is configured with a
// production audience but the locally running MCP server has a different base
// URL (and thus audience), you can use WithExtraAudience (multiple times if
// needed) to accept additional audience values beyond the primary. Avoid
// broadening accepted audiences in production unless intentionally operating
// in a multi-audience model.
func NewFromDiscovery(ctx context.Context, issuer string, audience string, opts ...AccessTokenAuthOption) (SecurityProvider, error) {
	cfg := jwtauth.DefaultConfig()
	cfg.Issuer = issuer
	cfg.ExpectedAudiences = []string{audience}
	for _, opt := range opts {
		opt(cfg)
	}
	if len(cfg.ExpectedAudiences) == 0 || cfg.ExpectedAudiences[0] == "" {
		return nil, errors.New("audience is required")
	}
	internal, err := jwtauth.NewFromDiscovery(ctx, cfg)
	if err != nil {
		return nil, err
	}
	sec := buildSecurityConfig(cfg, internal)
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
