package auth

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ggoodman/mcp-server-go/internal/jwtauth"
)

func TestStaticScopes(t *testing.T) {
	tests := []struct {
		name       string
		static     []string
		discovered []string
		want       []string
	}{
		{
			name:       "returns static scopes ignoring discovered",
			static:     []string{"mcp:read", "mcp:write"},
			discovered: []string{"openid", "profile", "email"},
			want:       []string{"mcp:read", "mcp:write"},
		},
		{
			name:       "empty static scopes",
			static:     []string{},
			discovered: []string{"openid", "profile"},
			want:       []string{},
		},
		{
			name:       "nil discovered scopes",
			static:     []string{"custom:scope"},
			discovered: nil,
			want:       []string{"custom:scope"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := StaticScopes(tt.static...)
			got := fn(tt.discovered)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("StaticScopes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStaticScopes_DefensiveCopy(t *testing.T) {
	// Verify that StaticScopes makes a defensive copy and doesn't alias
	input := []string{"scope1", "scope2"}
	fn := StaticScopes(input...)

	// Mutate the original input
	input[0] = "modified"

	// The function should return the original values
	got := fn([]string{"irrelevant"})
	want := []string{"scope1", "scope2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StaticScopes() was affected by mutation = %v, want %v", got, want)
	}
}

func TestFilterScopes(t *testing.T) {
	tests := []struct {
		name       string
		predicate  func(string) bool
		discovered []string
		want       []string
	}{
		{
			name: "filter out internal scopes",
			predicate: func(s string) bool {
				return !strings.HasPrefix(s, "internal:")
			},
			discovered: []string{"openid", "internal:admin", "profile", "internal:debug"},
			want:       []string{"openid", "profile"},
		},
		{
			name: "keep only mcp scopes",
			predicate: func(s string) bool {
				return strings.HasPrefix(s, "mcp:")
			},
			discovered: []string{"openid", "mcp:read", "mcp:write", "profile"},
			want:       []string{"mcp:read", "mcp:write"},
		},
		{
			name: "filter all scopes",
			predicate: func(s string) bool {
				return false
			},
			discovered: []string{"openid", "profile", "email"},
			want:       []string{},
		},
		{
			name: "keep all scopes",
			predicate: func(s string) bool {
				return true
			},
			discovered: []string{"openid", "profile", "email"},
			want:       []string{"openid", "profile", "email"},
		},
		{
			name: "empty discovered scopes",
			predicate: func(s string) bool {
				return true
			},
			discovered: []string{},
			want:       []string{},
		},
		{
			name: "nil discovered scopes",
			predicate: func(s string) bool {
				return true
			},
			discovered: nil,
			want:       []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := FilterScopes(tt.predicate)
			got := fn(tt.discovered)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FilterScopes() = %v, want %v", got, tt.want)
			}
		})
	}
}

type fakeDiscovery struct {
	scopes []string
}

func (f fakeDiscovery) AuthorizationEndpoint() string      { return "" }
func (f fakeDiscovery) TokenEndpoint() string              { return "" }
func (f fakeDiscovery) ResponseTypes() []string            { return nil }
func (f fakeDiscovery) Scopes() []string                   { return append([]string(nil), f.scopes...) }
func (f fakeDiscovery) GrantTypes() []string               { return nil }
func (f fakeDiscovery) ResponseModes() []string            { return nil }
func (f fakeDiscovery) CodeChallengeMethods() []string     { return nil }
func (f fakeDiscovery) TokenEndpointAuthMethods() []string { return nil }
func (f fakeDiscovery) TokenEndpointAuthAlgs() []string    { return nil }
func (f fakeDiscovery) ServiceDocumentation() string       { return "" }
func (f fakeDiscovery) PolicyURI() string                  { return "" }
func (f fakeDiscovery) TosURI() string                     { return "" }
func (f fakeDiscovery) RegistrationEndpoint() string       { return "" }

func TestWithAdvertisedScopes(t *testing.T) {
	tests := []struct {
		name       string
		cfg        func(*jwtauth.Config)
		discovered []string
		want       []string
	}{
		{
			name:       "default uses discovered scopes",
			cfg:        func(c *jwtauth.Config) {},
			discovered: []string{"openid", "profile"},
			want:       []string{"openid", "profile"},
		},
		{
			name: "static scopes via helper",
			cfg: func(c *jwtauth.Config) {
				WithAdvertisedScopes(StaticScopes("custom:read", "custom:write"))(c)
			},
			discovered: []string{"openid", "profile"},
			want:       []string{"custom:read", "custom:write"},
		},
		{
			name: "filter scopes via helper",
			cfg: func(c *jwtauth.Config) {
				WithAdvertisedScopes(FilterScopes(func(s string) bool {
					return !strings.HasPrefix(s, "internal:")
				}))(c)
			},
			discovered: []string{"openid", "internal:admin", "profile"},
			want:       []string{"openid", "profile"},
		},
		{
			name: "custom transform - merge and dedupe",
			cfg: func(c *jwtauth.Config) {
				WithAdvertisedScopes(func(discovered []string) []string {
					combined := append(discovered, "custom:scope")
					seen := make(map[string]bool)
					result := []string{}
					for _, s := range combined {
						if !seen[s] {
							seen[s] = true
							result = append(result, s)
						}
					}
					return result
				})(c)
			},
			discovered: []string{"openid", "profile"},
			want:       []string{"openid", "profile", "custom:scope"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := jwtauth.DefaultConfig()
			cfg.Issuer = "issuer"
			cfg.ExpectedAudiences = []string{"aud"}
			// Apply test-specific configuration (including advertised scopes transform).
			if tt.cfg != nil {
				tt.cfg(cfg)
			}

			sec := buildSecurityConfig(cfg, fakeDiscovery{scopes: tt.discovered})
			if sec.OIDC == nil {
				t.Fatalf("expected OIDC metadata to be populated")
			}
			got := sec.OIDC.ScopesSupported
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ScopesSupported = %v, want %v", got, tt.want)
			}
		})
	}
}
