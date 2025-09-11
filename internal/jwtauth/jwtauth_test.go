package jwtauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
)

type mockOIDC struct {
	srv       *httptest.Server
	issuer    string
	jwksPath  string
	keysJSON  []byte
	metaExtra map[string]any
}

func newMockOIDC(t *testing.T, keysJSON []byte, metaExtra map[string]any) *mockOIDC {
	t.Helper()
	m := &mockOIDC{jwksPath: "/keys", metaExtra: metaExtra}
	handler := http.NewServeMux()
	handler.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		meta := map[string]any{
			"issuer":   m.issuer,
			"jwks_uri": m.issuer + m.jwksPath,
			// minimal extras to look realistic
			"response_types_supported": []string{"code"},
		}
		for k, v := range m.metaExtra {
			meta[k] = v
		}
		_ = json.NewEncoder(w).Encode(meta)
	})
	handler.HandleFunc(m.jwksPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(keysJSON)
	})
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set issuer lazily to current server URL
		if m.issuer == "" {
			m.issuer = m.srv.URL
		}
		handler.ServeHTTP(w, r)
	}))
	m.issuer = m.srv.URL
	return m
}

func (m *mockOIDC) Close() { m.srv.Close() }

func genRSA(t *testing.T) (*rsa.PrivateKey, string, []byte) {
	t.Helper()
	pk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	kid := "test-key"
	jwk := jose.JSONWebKey{Key: &pk.PublicKey, KeyID: kid, Algorithm: "RS256", Use: "sig"}
	set := struct {
		Keys []jose.JSONWebKey `json:"keys"`
	}{Keys: []jose.JSONWebKey{jwk}}
	b, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	return pk, kid, b
}

func signToken(t *testing.T, pk *rsa.PrivateKey, kid string, headerTyp string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	if headerTyp != "" {
		tok.Header["typ"] = headerTyp
	}
	s, err := tok.SignedString(pk)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func baseConfig(issuer, aud string) *Config {
	cfg := DefaultConfig()
	cfg.Issuer = issuer
	cfg.ExpectedAudience = aud
	cfg.Leeway = 0
	return cfg
}

func TestAuthenticator_HappyPath(t *testing.T) {
	pk, kid, jwks := genRSA(t)
	oidc := newMockOIDC(t, jwks, nil)
	defer oidc.Close()

	aud := "https://api.example.com/mcp"
	cfg := baseConfig(oidc.issuer, aud)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := NewFromDiscovery(ctx, cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   oidc.issuer,
		"sub":   "user-123",
		"aud":   aud,
		"exp":   now.Add(time.Hour).Unix(),
		"iat":   now.Unix(),
		"scope": "mcp:read mcp:write",
	}
	tok := signToken(t, pk, kid, "at+jwt", claims)

	ui, err := a.CheckAuthentication(ctx, tok)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if ui.UserID() != "user-123" {
		t.Fatalf("want sub user-123, got %s", ui.UserID())
	}

	var out struct {
		Scope string `json:"scope"`
	}
	if err := ui.Claims(&out); err != nil {
		t.Fatalf("claims: %v", err)
	}
	if out.Scope != "mcp:read mcp:write" {
		t.Fatalf("scope roundtrip mismatch: %q", out.Scope)
	}
}

func TestAuthenticator_AudienceArray(t *testing.T) {
	pk, kid, jwks := genRSA(t)
	oidc := newMockOIDC(t, jwks, nil)
	defer oidc.Close()

	aud := "https://api.example.com/mcp"
	cfg := baseConfig(oidc.issuer, aud)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := NewFromDiscovery(ctx, cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss": oidc.issuer,
		"sub": "user-123",
		"aud": []string{"https://other", aud},
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	tok := signToken(t, pk, kid, "at+jwt", claims)

	if _, err := a.CheckAuthentication(ctx, tok); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestAuthenticator_InsufficientScope(t *testing.T) {
	pk, kid, jwks := genRSA(t)
	oidc := newMockOIDC(t, jwks, nil)
	defer oidc.Close()

	aud := "https://api.example.com/mcp"
	cfg := baseConfig(oidc.issuer, aud)
	cfg.RequiredScopes = []string{"mcp:write", "mcp:admin"}
	// all required by default
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := NewFromDiscovery(ctx, cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   oidc.issuer,
		"sub":   "user-123",
		"aud":   aud,
		"exp":   now.Add(time.Hour).Unix(),
		"iat":   now.Unix(),
		"scope": "mcp:write", // missing mcp:admin
	}
	tok := signToken(t, pk, kid, "at+jwt", claims)

	_, err = a.CheckAuthentication(ctx, tok)
	if !errors.Is(err, ErrInsufficientScope) {
		t.Fatalf("want ErrInsufficientScope, got %v", err)
	}
}

func TestAuthenticator_InvalidTyp(t *testing.T) {
	pk, kid, jwks := genRSA(t)
	oidc := newMockOIDC(t, jwks, nil)
	defer oidc.Close()

	aud := "https://api.example.com/mcp"
	cfg := baseConfig(oidc.issuer, aud)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := NewFromDiscovery(ctx, cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss": oidc.issuer,
		"sub": "user-123",
		"aud": aud,
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	tok := signToken(t, pk, kid, "JWT", claims) // wrong typ

	_, err = a.CheckAuthentication(ctx, tok)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestAuthenticator_IssuerMismatch(t *testing.T) {
	pk, kid, jwks := genRSA(t)
	oidc := newMockOIDC(t, jwks, nil)
	defer oidc.Close()

	aud := "https://api.example.com/mcp"
	cfg := baseConfig(oidc.issuer, aud)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := NewFromDiscovery(ctx, cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss": "https://evil.example.com", // mismatch
		"sub": "user-123",
		"aud": aud,
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	tok := signToken(t, pk, kid, "at+jwt", claims)

	_, err = a.CheckAuthentication(ctx, tok)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}
