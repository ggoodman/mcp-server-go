package jwtauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	keyfunc "github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// StaticConfig controls validation for manual (non-discovery) JWT access tokens.
// Caller supplies issuer, one or more expected audiences, and JWKS URI.
type StaticConfig struct {
	Issuer            string
	ExpectedAudiences []string
	AllowedAlgs       []string
	Leeway            time.Duration
}

// DefaultStaticConfig returns a StaticConfig with safe algorithm + leeway defaults.
func DefaultStaticConfig() *StaticConfig {
	return &StaticConfig{AllowedAlgs: []string{"RS256"}, Leeway: 60 * time.Second}
}

type staticAuthenticator struct {
	cfg     *StaticConfig
	keyfunc jwt.Keyfunc
}

// NewStatic constructs an authenticator that validates RFC 9068 JWT access tokens
// against a statically configured issuer, audiences and JWKS URI (no discovery).
func NewStatic(ctx context.Context, cfg *StaticConfig, jwksURI string) (*staticAuthenticator, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if cfg.Issuer == "" {
		return nil, errors.New("issuer is required")
	}
	if len(cfg.ExpectedAudiences) == 0 {
		return nil, errors.New("at least one expected audience required")
	}
	if jwksURI == "" {
		return nil, errors.New("jwks uri required")
	}
	if len(cfg.AllowedAlgs) == 0 {
		cfg.AllowedAlgs = []string{"RS256"}
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = 60 * time.Second
	}

	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURI})
	if err != nil {
		return nil, fmt.Errorf("jwks init failed: %w", err)
	}

	return &staticAuthenticator{cfg: cfg, keyfunc: func(t *jwt.Token) (any, error) {
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
	}}, nil
}

// CheckAuthentication implements the Authenticator interface (shared contract).
func (a *staticAuthenticator) CheckAuthentication(ctx context.Context, tok string) (UserInfo, error) {
	if tok == "" {
		return nil, errors.New("empty token")
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods(a.cfg.AllowedAlgs),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(a.cfg.Issuer),
		jwt.WithLeeway(a.cfg.Leeway),
	)
	parsed, err := parser.Parse(tok, a.keyfunc)
	if err != nil {
		return nil, fmt.Errorf("%w: token parse/verify failed: %v", ErrUnauthorized, err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid claims type")
	}
	// Audience intersection check (string or array).
	if !audIntersects(claims["aud"], a.cfg.ExpectedAudiences) {
		return nil, fmt.Errorf("%w: audience mismatch", ErrUnauthorized)
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, fmt.Errorf("%w: missing sub", ErrUnauthorized)
	}
	return &userInfo{sub: sub, claims: claims}, nil
}

func audIntersects(aud any, wants []string) bool {
	wantSet := map[string]struct{}{}
	for _, w := range wants {
		wantSet[w] = struct{}{}
	}
	switch v := aud.(type) {
	case string:
		_, ok := wantSet[v]
		return ok
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				if _, ok2 := wantSet[s]; ok2 {
					return true
				}
			}
		}
	case []string:
		for _, s := range v {
			if _, ok := wantSet[s]; ok {
				return true
			}
		}
	}
	return false
}

// Ensure staticAuthenticator satisfies the same interface expected by adapters.
var _ Authenticator = (*staticAuthenticator)(nil)
