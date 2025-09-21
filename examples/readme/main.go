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
	authenticator, err := auth.NewFromDiscovery(
		ctx,
		issuer,
		auth.WithExpectedAudience(publicEndpoint), // audience check (use your public MCP endpoint)
		auth.WithLeeway(2*time.Minute),
	)
	if err != nil {
		panic(err)
	}

	// 5) Drop-in handler
	h, err := streaminghttp.New(
		ctx,
		publicEndpoint,
		host,
		server,
		authenticator,
		streaminghttp.WithServerName("My MCP Server"),
		streaminghttp.WithLogger(defaultLogger()),
		streaminghttp.WithAuthorizationServerDiscovery(issuer),
	)
	if err != nil {
		panic(err)
	}

	// 6) Serve
	http.ListenAndServe("127.0.0.1:8080", h)
}
