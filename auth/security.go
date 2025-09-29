package auth

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/ggoodman/mcp-server-go/internal/jwtauth"
)

// SecurityConfig is the unified, immutable configuration describing how this
// resource validates and advertises bearer token authentication. It collapses
// what was previously spread across discovery/manual transport options and
// authenticator construction.
//
// A zero value is invalid; use NewSecurityConfig or populate required fields
// then call Validate.
//
// Breaking change: this type intentionally replaces multiple prior option
// pathways (separate discovery/manual transport options and audience settings)
// with a single explicit value to eliminate drift.
//
// Fields that are not used for enforcement (e.g. OIDCExtra) may be omitted.
type SecurityConfig struct {
	Issuer      string
	Audiences   []string
	AllowedAlgs []string // default: ["RS256"] if empty
	JWKSURL     string   // optional override / filled by discovery

	Leeway     time.Duration // clock skew tolerance (default 60s)
	EnforceExp bool          // default true
	EnforceNbf bool          // default true
	Advertise  bool          // default true (transport may publish metadata)

	OIDC *OIDCExtra // optional extended metadata for advertisement only
}

// OIDCExtra carries optional OpenID / OAuth authorization server metadata we
// surface for client bootstrapping. None of these fields are required for
// token validation.
type OIDCExtra struct {
	// AuthorizationEndpoint and TokenEndpoint are derived from OIDC discovery
	// (/.well-known/openid-configuration) when using discovery-based
	// authenticators. They are advertisement-only and never used for access
	// token validation in this process.
	AuthorizationEndpoint                      string
	TokenEndpoint                              string
	ScopesSupported                            []string
	ResponseTypesSupported                     []string
	GrantTypesSupported                        []string
	ResponseModesSupported                     []string
	CodeChallengeMethodsSupported              []string
	TokenEndpointAuthMethodsSupported          []string
	TokenEndpointAuthSigningAlgValuesSupported []string
	ServiceDocumentation                       string
	OpPolicyURI                                string
	OpTosURI                                   string
}

// Normalize fills defaults without mutating caller copies elsewhere.
func (c *SecurityConfig) Normalize() {
	if len(c.AllowedAlgs) == 0 {
		c.AllowedAlgs = []string{"RS256"}
	}
	if c.Leeway == 0 {
		c.Leeway = 60 * time.Second
	}
	// Default advertise true unless explicitly set false.
	// (Zero value bool is false, so treat "unset" as true only if we want advertisement by default.)
	// We flip it only if user left it default (false) AND there is meaningful data to share.
	if !c.Advertise {
		c.Advertise = true
	}
}

// Validate returns an error if required invariants are not met.
func (c SecurityConfig) Validate() error {
	if c.Issuer == nilString {
		return errors.New("security: issuer required")
	}
	if len(c.Audiences) == 0 {
		return errors.New("security: at least one audience required")
	}
	for _, a := range c.Audiences {
		if a == "" {
			return errors.New("security: empty audience entry")
		}
	}
	return nil
}

const nilString = ""

// Copy returns a deep copy safe for mutation by the caller.
func (c SecurityConfig) Copy() SecurityConfig {
	dup := c
	dup.Audiences = append([]string(nil), c.Audiences...)
	dup.AllowedAlgs = append([]string(nil), c.AllowedAlgs...)
	if c.OIDC != nil {
		ox := *c.OIDC
		ox.AuthorizationEndpoint = c.OIDC.AuthorizationEndpoint
		ox.TokenEndpoint = c.OIDC.TokenEndpoint
		ox.ScopesSupported = append([]string(nil), ox.ScopesSupported...)
		ox.ResponseTypesSupported = append([]string(nil), c.OIDC.ResponseTypesSupported...)
		ox.GrantTypesSupported = append([]string(nil), c.OIDC.GrantTypesSupported...)
		ox.ResponseModesSupported = append([]string(nil), c.OIDC.ResponseModesSupported...)
		ox.CodeChallengeMethodsSupported = append([]string(nil), c.OIDC.CodeChallengeMethodsSupported...)
		ox.TokenEndpointAuthMethodsSupported = append([]string(nil), ox.TokenEndpointAuthMethodsSupported...)
		ox.TokenEndpointAuthSigningAlgValuesSupported = append([]string(nil), ox.TokenEndpointAuthSigningAlgValuesSupported...)
		dup.OIDC = &ox
	}
	return dup
}

// NewManualJWTAuthenticator constructs a JWT access token authenticator using
// this security configuration without performing OIDC discovery. It expects:
//   - c.Issuer (non-empty)
//   - at least one audience in c.Audiences
//   - c.JWKSURL (non-empty)
//
// AllowedAlgs and Leeway are honored (defaults applied via Normalize if needed).
// OIDC advertisement fields (AuthorizationEndpoint, TokenEndpoint, etc.) are
// not required for validation but may be present for metadata serving.
func (c SecurityConfig) NewManualJWTAuthenticator(ctx context.Context) (SecurityProvider, error) {
	cc := c.Copy()
	cc.Normalize()
	if err := cc.Validate(); err != nil {
		return nil, err
	}
	if cc.JWKSURL == "" {
		return nil, errors.New("security: JWKSURL required for manual JWT authenticator")
	}

	// Build static validator (no discovery) using internal jwtauth.
	sc := &jwtauth.StaticConfig{Issuer: cc.Issuer, ExpectedAudiences: append([]string(nil), cc.Audiences...), AllowedAlgs: append([]string(nil), cc.AllowedAlgs...), Leeway: cc.Leeway}
	a, err := jwtauth.NewStatic(ctx, sc, cc.JWKSURL)
	if err != nil {
		return nil, err
	}
	// Wrap to expose SecurityDescriptor + Authenticator.
	return &adapter{a: a, sec: cc}, nil
}

// EqualCore returns true if the core enforcement identity (issuer + audiences) matches.
func (c SecurityConfig) EqualCore(o SecurityConfig) bool {
	if c.Issuer != o.Issuer {
		return false
	}
	if len(c.Audiences) != len(o.Audiences) {
		return false
	}
	ac := append([]string(nil), c.Audiences...)
	bc := append([]string(nil), o.Audiences...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}

// SecurityDescriptor exposes security configuration for transports to advertise.
type SecurityDescriptor interface{ SecurityConfig() SecurityConfig }

// SecurityProvider combines validation + descriptor. Returned by constructors.
type SecurityProvider interface {
	Authenticator
	SecurityDescriptor
}
