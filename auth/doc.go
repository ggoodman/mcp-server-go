// Package auth provides pluggable authentication primitives used by the
// streaming HTTP transport. It focuses on bearer token (JWT) verification
// for MCP servers that delegate authorization to an external OAuth 2.0 / OIDC
// authorization server.
//
// The public surface intentionally stays small: an Authenticator validates an
// incoming bearer token string and returns a UserInfo (or an error). The
// transport is responsible for extracting the token from the HTTP request and
// mapping sentinel errors into protocol-specific challenges.
//
// # Access Token Authentication
//
// NewFromDiscovery constructs an Authenticator that validates RFC 9068
// access tokens using OpenID Connect discovery to obtain the issuer's JWKS
// and metadata. Callers configure validation requirements via functional
// options (expected audience, required scopes, leeway, allowed algorithms).
//
// Example:
//
//	ctx := context.Background()
//	authn, err := auth.NewFromDiscovery(ctx, "https://issuer.example", "https://mcp.example/api",
//	    auth.WithRequiredScopes("mcp:read", "mcp:write"),
//	)
//	if err != nil { log.Fatal(err) }
//
//	// Later inside request handling (pseudocode):
//	ui, err := authn.CheckAuthentication(r.Context(), bearerToken)
//	if errors.Is(err, auth.ErrUnauthorized) { /* map to 401 challenge */ }
//	if errors.Is(err, auth.ErrInsufficientScope) { /* map to insufficient scope */ }
//	userID := ui.UserID()
//
// # Scopes
//
// WithRequiredScopes enforces that all provided scopes are present in the
// token's space-delimited scope claim; WithAnyRequiredScope relaxes this so
// at least one matches. Only one of these should be used per Authenticator
// configuration (subsequent calls overwrite scope mode).
//
// Algorithms & Clock Skew
//
// By default only RS256 is accepted. Use WithAllowedAlgs to broaden the set.
// WithLeeway adds tolerance for clock skew when validating exp/iat/nbf.
//
// # Errors
//
// ErrUnauthorized signals the token is invalid (signature, expiry, audience,
// etc.). ErrInsufficientScope signals successful authentication but missing
// required scope(s). Additional HTTP challenge detail is surfaced via the
// internal AuthenticationResult type used by the transport layer; typical
// library users do not construct those directly.
package auth
