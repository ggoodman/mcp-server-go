package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/sessions/redishost"
	"github.com/ggoodman/mcp-server-go/streaminghttp"
)

func main() {
	ctx := context.Background()

	publicEndpoint := os.Getenv("MCP_PUBLIC_ENDPOINT") // e.g. https://mcp.example.com/mcp
	issuer := os.Getenv("OIDC_ISSUER")                 // your OAuth/OIDC issuer URL

	// 1) Session host for horizontal scale (Redis)
	host, err := redishost.New(os.Getenv("REDIS_ADDR"))
	if err != nil {
		panic(err)
	}
	defer host.Close()

	// 2) Construct server (separated into mcp_server.go)
	server := NewExampleServer()

	// 4) OAuth2/OIDC JWT access token validation (RFC 9068)
	// Discovery path (strict: requires jwks_uri, authorization_endpoint, token_endpoint, response_types_supported)
	authenticator, err := auth.NewFromDiscovery(
		ctx,
		issuer,
		publicEndpoint, // audience (expected aud in tokens)
		auth.WithLeeway(2*time.Minute),
	)
	if err != nil {
		panic(err)
	}

	// --- Manual static configuration alternative (uncomment to use) ---
	// If you cannot or do not want to perform discovery, construct a SecurityConfig
	// and build a manual authenticator. Nothing is synthesized: only fields you
	// populate in OIDCExtra are advertised at well-known endpoints.
	//
	// jwksURL := os.Getenv("OIDC_JWKS_URL")
	// if jwksURL != "" {
	//     sec := auth.SecurityConfig{
	//         Issuer:    issuer,
	//         Audiences: []string{publicEndpoint},
	//         JWKSURL:   jwksURL,
	//         Advertise: true,
	//         OIDC: &auth.OIDCExtra{
	//             ResponseTypesSupported: []string{"code"}, // required for clients to know flow
	//         },
	//     }
	//     sec.Normalize()
	//     if sp, err := sec.NewManualJWTAuthenticator(ctx); err == nil {
	//         authenticator = sp
	//     } else {
	//         panic(err)
	//     }
	// }

	// 5) Drop-in handler
	h, err := streaminghttp.New(
		ctx,
		publicEndpoint,
		host,
		server,
		authenticator, // security metadata inferred from authenticator's SecurityDescriptor
		streaminghttp.WithServerName("My MCP Server"),
		streaminghttp.WithLogger(defaultLogger()),
	)
	if err != nil {
		panic(err)
	}

	// 6) Serve
	http.ListenAndServe("127.0.0.1:8080", h)
}
