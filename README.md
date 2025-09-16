# MCP Streaming HTTP for Go

A drop-in `http.Handler` that implements the Model Context Protocol (MCP) streaming HTTP transport. Mount it in front of your server to get an HTTP/SSE interface that works in single-instance and horizontally scaled deployments.

## Install

Go modules:

```
go get github.com/ggoodman/mcp-server-go
```

## Quick start

Build a server with typed tools and Redis-backed sessions, using OIDC discovery and JWT access token auth:

```go
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
	host, err := redishost.NewFromEnv()
	if err != nil {
		panic(err)
	}
	defer host.Close()

	// 2) Tools (auto-derived JSON Schema; strict by default)
	translate := mcpservice.NewTool[TranslateArgs](
		"translate",
		func(ctx context.Context, _ sessions.Session, args TranslateArgs) (*mcp.CallToolResult, error) {
			return mcpservice.TextResult("Translated to " + args.To + ": " + args.Text), nil
		},
		mcpservice.WithToolDescription("Translate text to a target language."),
		// mcpserver.WithToolAllowAdditionalProperties(true), // opt-in to unknown fields
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
```

### Typed tools in a nutshell

- Define an args struct with `json` tags (and optional `jsonschema` tags for docs/enums).
- Use `mcpserver.NewTool[T]` to create a tool descriptor and handler.
- The library derives an MCP `inputSchema` from your struct and rejects unknown fields by default. Opt into leniency with `WithToolAllowAdditionalProperties(true)`.

## Manual authorization server configuration

If discovery isn’t available, pass a single `ManualOIDC` struct:

```go
h, err := streaminghttp.New(
  ctx,
  "https://api.example.com/mcp",
  host,
  server,
  authenticator,
  streaminghttp.WithServerName("Demo MCP"),
  streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{
    Issuer:  "https://issuer.example.com",
    JwksURI: "https://issuer.example.com/.well-known/jwks.json",
    ScopesSupported: []string{"mcp:read", "mcp:write"},
    // Bearer tokens are presented via the Authorization header.
    // PRM is constructed automatically; you usually don't need to tweak it.
    ServiceDocumentation: "https://docs.example.com/mcp",
    OpPolicyURI:          "https://example.com/policy",
    OpTosURI:             "https://example.com/tos",
  }),
)
```

Note: `WithAuthorizationServerDiscovery(issuer)` consumes OAuth 2.0 Authorization Server Metadata (RFC 8414). Supplying an OIDC issuer URL works because the OIDC discovery document contains the same fields used here (e.g., `jwks_uri`, `scopes_supported`).

## Static resources with listChanged

```go
sr := mcpserver.NewStaticResources(nil, nil, nil)
server := mcpserver.NewServer(
  mcpserver.WithResourcesOptions(mcpserver.WithStaticResourceContainer(sr)),
)
// Mutations on sr trigger list_changed; the handler fans out via internal events.
```

## API shape

```go
// Typed tools
func NewTool[A any](
  name string,
  fn func(ctx context.Context, session sessions.Session, args A) (*mcp.CallToolResult, error),
  opts ...ToolOption,
) mcpserver.StaticTool

type ToolOption func(*toolConfig)
func WithToolDescription(desc string) ToolOption
func WithToolAllowAdditionalProperties(allow bool) ToolOption

// Legacy helper retained for compatibility
func TypedTool[A any](desc mcp.Tool, fn func(ctx context.Context, session sessions.Session, args A) (*mcp.CallToolResult, error)) mcpserver.StaticTool

func New(
  ctx context.Context,
  publicEndpoint string,
  host sessions.SessionHost,
  server mcpserver.ServerCapabilities,
  authenticator auth.Authenticator,
  opts ...Option,
) (*StreamingHTTPHandler, error)

// Options
func WithServerName(name string) Option
func WithLogger(h slog.Handler) Option
func WithAuthorizationServerDiscovery(authorizationServerURL string) Option
func WithManualOIDC(cfg ManualOIDC) Option

// ManualOIDC
type ManualOIDC struct {
  Issuer, JwksURI string
  ScopesSupported []string
  TokenEndpointAuthMethodsSupported []string
  TokenEndpointAuthSigningAlgValuesSupported []string
  ServiceDocumentation string
  OpPolicyURI string
  OpTosURI string
}
```

## Session isolation and lifecycle

- Session binding: `MCP-Session-ID` is bound to the authenticated principal. All GET/POST/DELETE requests validate that the session belongs to the current user; mismatches return 404 without revealing existence. When JWS-signed session IDs are enabled, the token encodes user, issuer, epoch, and negotiated protocol version.
- Initialization: Creating a session via `initialize` returns the session ID in the `MCP-Session-ID` response header, along with an SSE `response` event.
- Protocol version: After initialization, requests must include `MCP-Protocol-Version` matching the negotiated version, otherwise the server returns 428 (missing) or 412 (mismatch).
- Stream consumption: GET requires `MCP-Session-ID` and optionally `Last-Event-ID` to resume. If omitted, delivery starts from the next message.
- Teardown and GC: DELETE revokes the session (precise revocation with a bounded TTL; default 24h), bumps per-user epoch when supported by the host, deletes host-side delivery artifacts, and tears down process-local forwarders. Sessions do not auto-expire by default.

## Operational limits and tuning

- Request and response formats:
  - POST requires `Content-Type: application/json` and `Accept: text/event-stream`. Only single JSON-RPC messages are supported (no batching).
  - GET streams events as SSE. Each event is flushed after write.
- Sizes and backpressure:
  - No library-enforced request body cap; enforce limits at your proxy (e.g., NGINX) or wrap with `http.MaxBytesReader` in your app.
  - SSE uses flush-per-event. Slow clients will apply TCP backpressure; there’s no extra application buffer beyond the session host queue.
- Persistence:
  - Session message streams are retained until DELETE. If you do not DELETE, host-side streams may accumulate.
- Timeouts and heartbeats:
  - The handler doesn’t set custom read/write timeouts or heartbeats; rely on your server/proxy configuration. Consider enabling keep-alives and appropriate idle timeouts for SSE stability.
- Recommendations:
  - Always DELETE sessions when done to free resources.
  - Track and send `Last-Event-ID` on reconnects to avoid gaps.
  - Keep messages small and chunk large results.

## Notes

- The handler registers path-first routes using `http.ServeMux` based on your `publicEndpoint` path. When mounting behind a proxy, ensure Host is preserved if you enable host matching.
- PRM is served at `/.well-known/oauth-protected-resource{path}` under the same host.
- For horizontal scale, use the Redis host in `sessions/redishost`.
- Typed tools use `github.com/invopop/jsonschema` under the hood to derive schemas, and then down-convert to MCP's simplified `ToolInputSchema`.

## HTTP contract

- POST {publicPath}

  - Headers: `Authorization: Bearer <token>`, `Content-Type: application/json`, `Accept: text/event-stream`
  - Body: single JSON-RPC message (no batching)
  - If no `MCP-Session-ID` header, the message MUST be `initialize`; response is an SSE `response` event with `InitializeResult`.
  - For requests with an ID, the response is an SSE `response` event.

- GET {publicPath}

  - Headers: `Authorization: Bearer <token>`, `Accept: text/event-stream`, `MCP-Session-ID: <id>`, optional `Last-Event-ID`
  - Stream: SSE events of type `request`, `notification`, and `response`.

- DELETE {publicPath}

  - Headers: `Authorization: Bearer <token>`, `MCP-Session-ID: <id>`
  - Result: 204 on success; cleans server- and process-local session resources.

- Protocol version
  - Clients include `MCP-Protocol-Version: <version>` after `initialize` per spec; the server may enforce version matching.
