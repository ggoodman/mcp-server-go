package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/redishost"
	"github.com/ggoodman/mcp-server-go/streaminghttp"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var (
	// MCP_PUBLIC_ENDPOINT should be the externally visible base URL of your server
	// (used as the expected audience for access tokens). Example: https://mcp.example.com
	publicEndpoint = envOr("MCP_PUBLIC_ENDPOINT", "http://127.0.0.1:8080/mcp")

	// OIDC_ISSUER should be your OAuth2/OIDC issuer URL. For local/dev, point this
	// to a local IdP or mock OIDC provider if you have one running.
	issuer = envOr("OIDC_ISSUER", "http://127.0.0.1:8081/")
)

type TranslateArgs struct {
	Text string `json:"text" jsonschema:"minLength=1,description=Text to translate"`
	To   string `json:"to"   jsonschema:"enum=en,enum=fr,enum=es,description=Target language (ISO 639-1)"`
}

func main() {
	ctx := context.Background()

	// 1) Session host for horizontal scale (Redis)
	host, err := redishost.New(envOr("REDIS_ADDR", "127.0.0.1:6379"))
	if err != nil {
		panic(err)
	}
	defer host.Close()

	// 2) Tools (auto-derived JSON Schema; strict by default)
	translate := mcpservice.NewTool[TranslateArgs](
		"translate",
		func(ctx context.Context, _ sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[TranslateArgs]) error {
			a := r.Args()
			if a.Text == "" {
				w.SetError(true)
				_ = w.AppendText("text is required")
				return nil
			}
			_ = w.AppendText("Translated to " + a.To + ": " + a.Text)
			return nil
		},
		mcpservice.WithToolDescription("Translate text to a target language."),
		// mcpservice.WithToolAllowAdditionalProperties(true), // opt-in to unknown fields
	)
	tools := mcpservice.NewStaticTools(translate)

	// 3) Server capabilities
	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "my-mcp", Version: "1.0.0"}),
		mcpservice.WithToolsOptions(mcpservice.WithStaticToolsContainer(tools)),
	)

	// 4) Drop-in OAuth2/OIDC JWT access token auth (RFC 9068): discovery + JWKS
	authenticator, err := auth.NewFromDiscovery(
		ctx,
		issuer,
		auth.WithExpectedAudience(publicEndpoint),
		auth.WithLeeway(2*time.Minute),
		// auth.WithRequiredScopes("mcp:read","mcp:write"), // optional
		// auth.WithAllowedAlgs("RS256","ES256"),          // optional
	)
	if err != nil {
		panic(err)
	}

	// 5) Build handler
	h, err := streaminghttp.New(
		ctx,
		publicEndpoint, // externally visible URL
		host,
		server,
		authenticator,
		streaminghttp.WithServerName("My MCP Server"),
		streaminghttp.WithLogger(slog.NewTextHandler(os.Stdout, nil)),
		streaminghttp.WithAuthorizationServerDiscovery(issuer),
	)
	if err != nil {
		panic(err)
	}

	// 6) Serve
	http.ListenAndServe("127.0.0.1:8080", h)
}
