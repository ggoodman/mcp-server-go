package jwtauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	keyfunc "github.com/MicahParks/keyfunc/v3"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
)

// Config controls validation behavior for access tokens.
// It is used by discovery-based authenticators to enforce issuer, audience,
// scope, algorithm, and clock-skew policies.
type Config struct {
	Issuer string
	// ExpectedAudiences contains the primary audience (index 0) followed by any
	// additional accepted audiences. The first entry SHOULD be the production
	// audience registered with the authorization server; subsequent entries are
	// primarily intended for local / testing scenarios where the served MCP
	// endpoint base URL differs from the production one. Avoid growing this set
	// in production unless deliberately operating a multi-audience design.
	ExpectedAudiences []string
	RequiredScopes    []string
	ScopeModeAny      bool // if true, any of RequiredScopes is sufficient; else all are required
	AllowedAlgs       []string
	Leeway            time.Duration
	// HintScopes carries an optional set of scopes that transports may echo
	// in WWW-Authenticate "scope" parameters when constructing Bearer
	// challenges. They are advisory only and do not affect token validation.
	HintScopes []string
}

// DefaultConfig returns a Config with safe defaults for algorithm and leeway.
func DefaultConfig() *Config {
	return &Config{
		AllowedAlgs: []string{"RS256"},
		Leeway:      60 * time.Second,
	}
}

// UserInfo is the internal user claims carrier for validated tokens.
// It mirrors the minimal contract needed by the public auth package.
type UserInfo interface {
	UserID() string
	Claims(ref any) error
}

// userInfo is the concrete implementation of UserInfo.
type userInfo struct {
	sub    string
	claims map[string]any
}

func (u *userInfo) UserID() string { return u.sub }
func (u *userInfo) Claims(ref any) error {
	b, err := json.Marshal(u.claims)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, ref)
}

// Authenticator validates access tokens and returns a minimal UserInfo
// that exposes the subject and access to raw claims. Implementations
// MUST perform signature, issuer, audience and time validations.
type Authenticator interface {
	CheckAuthentication(ctx context.Context, tok string) (UserInfo, error)
}

type discoveryAuthenticator struct {
	cfg     *Config
	issuer  string
	keyfunc jwt.Keyfunc
	// expected fields derived from discovery
	iss                   string
	authorizationEndpoint string
	tokenEndpoint         string
	registrationEndpoint  string
	responseTypes         []string
	scopes                []string
	grantTypes            []string
	responseModes         []string
	codeChallengeMethods  []string
	tokenAuthMethods      []string
	tokenAuthAlgs         []string
	serviceDoc            string
	policyURI             string
	tosURI                string
}

// DiscoveryMetadata exposes optional advertisement-only endpoints learned via
// OIDC discovery. Implementations may return empty strings if not applicable.
type DiscoveryMetadata interface {
	AuthorizationEndpoint() string
	TokenEndpoint() string
}

func (a *discoveryAuthenticator) AuthorizationEndpoint() string { return a.authorizationEndpoint }
func (a *discoveryAuthenticator) TokenEndpoint() string         { return a.tokenEndpoint }

// ErrUnauthorized indicates that the access token failed validation (e.g.,
// signature, issuer, audience, exp/nbf) and the request should be treated as
// unauthenticated.
var ErrUnauthorized = errors.New("jwtauth: unauthorized")

// ErrInsufficientScope indicates the token was valid but did not satisfy the
// required scopes policy; callers should respond with HTTP 403 where relevant.
var ErrInsufficientScope = errors.New("jwtauth: insufficient_scope")

// NewFromDiscovery performs OIDC discovery to obtain jwks_uri and issuer, and
// constructs an Authenticator that validates RFC 9068 access tokens using the
// configured policies in Config. JWKS keys are auto-refreshed.
func NewFromDiscovery(ctx context.Context, cfg *Config) (*discoveryAuthenticator, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if cfg.Issuer == "" {
		return nil, errors.New("issuer is required")
	}

	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery failed: %w", err)
	}
	var meta struct {
		Issuer        string   `json:"issuer"`
		JwksURI       string   `json:"jwks_uri"`
		Authorization string   `json:"authorization_endpoint"`
		Token         string   `json:"token_endpoint"`
		Registration  string   `json:"registration_endpoint"`
		ResponseTypes []string `json:"response_types_supported"`
		Scopes        []string `json:"scopes_supported"`
		GrantTypes    []string `json:"grant_types_supported"`
		ResponseModes []string `json:"response_modes_supported"`
		CodeChallenge []string `json:"code_challenge_methods_supported"`
		TokenAuth     []string `json:"token_endpoint_auth_methods_supported"`
		TokenAuthAlgs []string `json:"token_endpoint_auth_signing_alg_values_supported"`
		ServiceDoc    string   `json:"service_documentation"`
		PolicyURI     string   `json:"op_policy_uri"`
		TosURI        string   `json:"op_tos_uri"`
	}
	if err := provider.Claims(&meta); err != nil {
		return nil, fmt.Errorf("invalid discovery metadata: %w", err)
	}
	missing := []string{}
	if meta.JwksURI == "" {
		missing = append(missing, "jwks_uri")
	}
	if meta.Authorization == "" {
		missing = append(missing, "authorization_endpoint")
	}
	if meta.Token == "" {
		missing = append(missing, "token_endpoint")
	}
	if len(meta.ResponseTypes) == 0 {
		missing = append(missing, "response_types_supported")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("discovery incomplete: missing %s", strings.Join(missing, ", "))
	}

	// Auto-refreshing JWKS
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{meta.JwksURI})
	if err != nil {
		return nil, fmt.Errorf("jwks init failed: %w", err)
	}

	return &discoveryAuthenticator{
		cfg:    cfg,
		issuer: cfg.Issuer,
		keyfunc: func(t *jwt.Token) (any, error) {
			// Enforce allowed algs
			alg := t.Method.Alg()
			allowed := false
			for _, a := range cfg.AllowedAlgs {
				if alg == a {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, fmt.Errorf("disallowed alg: %s", alg)
			}
			return kf.Keyfunc(t)
		},
		iss:                   meta.Issuer,
		authorizationEndpoint: meta.Authorization,
		tokenEndpoint:         meta.Token,
		responseTypes:         append([]string(nil), meta.ResponseTypes...),
		registrationEndpoint:  meta.Registration,
		scopes:                append([]string(nil), meta.Scopes...),
		grantTypes:            append([]string(nil), meta.GrantTypes...),
		responseModes:         append([]string(nil), meta.ResponseModes...),
		codeChallengeMethods:  append([]string(nil), meta.CodeChallenge...),
		tokenAuthMethods:      append([]string(nil), meta.TokenAuth...),
		tokenAuthAlgs:         append([]string(nil), meta.TokenAuthAlgs...),
		serviceDoc:            meta.ServiceDoc,
		policyURI:             meta.PolicyURI,
		tosURI:                meta.TosURI,
	}, nil
}

// Extended discovery accessors used by outer layers to populate advertisement metadata.
func (a *discoveryAuthenticator) ResponseTypes() []string {
	return append([]string(nil), a.responseTypes...)
}
func (a *discoveryAuthenticator) Scopes() []string     { return append([]string(nil), a.scopes...) }
func (a *discoveryAuthenticator) GrantTypes() []string { return append([]string(nil), a.grantTypes...) }
func (a *discoveryAuthenticator) ResponseModes() []string {
	return append([]string(nil), a.responseModes...)
}
func (a *discoveryAuthenticator) CodeChallengeMethods() []string {
	return append([]string(nil), a.codeChallengeMethods...)
}
func (a *discoveryAuthenticator) TokenEndpointAuthMethods() []string {
	return append([]string(nil), a.tokenAuthMethods...)
}
func (a *discoveryAuthenticator) TokenEndpointAuthAlgs() []string {
	return append([]string(nil), a.tokenAuthAlgs...)
}
func (a *discoveryAuthenticator) ServiceDocumentation() string { return a.serviceDoc }
func (a *discoveryAuthenticator) PolicyURI() string            { return a.policyURI }
func (a *discoveryAuthenticator) TosURI() string               { return a.tosURI }
func (a *discoveryAuthenticator) RegistrationEndpoint() string { return a.registrationEndpoint }

func (a *discoveryAuthenticator) CheckAuthentication(ctx context.Context, tok string) (UserInfo, error) {
	if tok == "" {
		return nil, errors.New("empty token")
	}

	// Build parser options. If exactly one expected audience is configured we
	// can leverage the parser's built-in audience enforcement. If multiple are
	// present we perform intersection logic after parsing.
	opts := []jwt.ParserOption{
		jwt.WithValidMethods(a.cfg.AllowedAlgs),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(a.iss),
		jwt.WithLeeway(a.cfg.Leeway),
	}
	if len(a.cfg.ExpectedAudiences) == 1 {
		opts = append(opts, jwt.WithAudience(a.cfg.ExpectedAudiences[0]))
	}
	parser := jwt.NewParser(opts...)

	parsed, err := parser.Parse(tok, a.keyfunc)
	if err != nil {
		return nil, fmt.Errorf("%w: token parse/verify failed: %v", ErrUnauthorized, err)
	}

	// Header checks (RFC 9068 typ)
	if typ, _ := parsed.Header["typ"].(string); typ != "at+jwt" && typ != "application/at+jwt" {
		return nil, fmt.Errorf("%w: invalid typ; want at+jwt", ErrUnauthorized)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid claims type")
	}

	now := time.Now().Add(a.cfg.Leeway)

	// Validate standard claims: iss, aud, exp is covered by Parse + WithExpirationRequired.
	if iss, _ := claims["iss"].(string); iss == "" || iss != a.iss {
		return nil, fmt.Errorf("%w: issuer mismatch", ErrUnauthorized)
	}
	if len(a.cfg.ExpectedAudiences) == 1 {
		if !audContains(claims["aud"], a.cfg.ExpectedAudiences[0]) {
			return nil, fmt.Errorf("%w: audience mismatch", ErrUnauthorized)
		}
	} else if !audIntersects(claims["aud"], a.cfg.ExpectedAudiences) {
		return nil, fmt.Errorf("%w: audience mismatch", ErrUnauthorized)
	}
	// Optional: iat presence sanity check if present
	if iatf, ok := claims["iat"].(float64); ok {
		// basic sanity: not too far in the future
		iat := time.Unix(int64(iatf), 0)
		if iat.After(now.Add(5 * time.Minute)) {
			return nil, fmt.Errorf("%w: iat too far in future", ErrUnauthorized)
		}
	}

	// Scope checks if configured
	if len(a.cfg.RequiredScopes) > 0 {
		scopeStr, _ := claims["scope"].(string)
		have := map[string]bool{}
		for _, s := range strings.Fields(scopeStr) {
			have[s] = true
		}
		if a.cfg.ScopeModeAny {
			ok := false
			for _, want := range a.cfg.RequiredScopes {
				if have[want] {
					ok = true
					break
				}
			}
			if !ok {
				return nil, ErrInsufficientScope
			}
		} else {
			for _, want := range a.cfg.RequiredScopes {
				if !have[want] {
					return nil, ErrInsufficientScope
				}
			}
		}
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, fmt.Errorf("%w: missing sub", ErrUnauthorized)
	}

	return &userInfo{sub: sub, claims: claims}, nil
}

func audContains(aud any, want string) bool {
	switch v := aud.(type) {
	case string:
		return v == want
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok && s == want {
				return true
			}
		}
	case []string:
		for _, s := range v {
			if s == want {
				return true
			}
		}
	}
	return false
}
