# MCP Streaming HTTP for Go

Drop-in `http.Handler` for the Model Context Protocol (MCP) Streaming HTTP transport — production-minded, horizontally scalable, and designed for both simple and complex servers.

- Implements the MCP streaming HTTP transport with Server-Sent Events (SSE)
- First-class authorization and session lifecycle handling
- Horizontal scale via a pluggable session host (memory or Redis)
- Static and dynamic capabilities for tools and resources

## Why this library

1. Horizontal scalability

   - Multiple instances can serve the same MCP endpoint and coordinate safely.
   - Per-session ordered messaging with resume using `Last-Event-ID`.
   - Pluggable cross-instance coordination via `sessions.SessionHost`.
     - In-memory host for single-process or tests: `sessions/memoryhost`.
     - Redis-backed host for multi-instance deployments: `sessions/redishost`.

2. Authorization and session management

   - Treats auth and sessions as first-class production concerns.
   - Out-of-the-box JWT access-token validation (RFC 9068) via OIDC discovery.
   - Protected Resource Metadata and Authorization Server Metadata endpoints are served for discovery. See `/.well-known/oauth-protected-resource/` and `/.well-known/oauth-authorization-server`.
   - Clean, minimal `auth.Authenticator` interface if you need custom logic.

3. Dynamic capabilities

   - Don’t assume your resources or tools are static. Implement dynamic listings and behaviors that pull from databases, APIs, or filesystems.
   - Prefer static containers when that’s simpler — you still get listChanged notifications and strict input schemas.
   - Swap static for dynamic later without rewriting your server.

4. Easy adoption, long-term power
   - Layered API: start with the drop-in handler, grow into custom capabilities.
   - Pragmatic defaults; minimal opinions. Security and isolation are non-negotiable.
   - Designed around the hard cases first so simple cases stay simple.

## Install

```bash
go get github.com/ggoodman/mcp-server-go
```

## Quick start (prod-friendly with OIDC)

This minimal server exposes a typed tool using the writer-based API, validates bearer tokens discovered from your issuer, and is horizontally scalable with Redis.

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

type TranslateArgs struct {
    Text string `json:"text" jsonschema:"minLength=1,description=Text to translate"`
    To   string `json:"to"   jsonschema:"enum=en,enum=fr,enum=es,description=Target language (ISO 639-1)"`
}

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

    // 2) Typed tool with strict input schema by default
    translate := mcpservice.NewTool[TranslateArgs](
        "translate",
        func(ctx context.Context, _ sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[TranslateArgs]) error {
            a := r.Args()
            _ = w.AppendText("Translated to " + a.To + ": " + a.Text)
            return nil
        },
        mcpservice.WithToolDescription("Translate text to a target language."),
    )
    tools := mcpservice.NewStaticTools(translate)

    // 3) Server capabilities
    server := mcpservice.NewServer(
        mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "my-mcp", Version: "1.0.0"}),
        mcpservice.WithToolsOptions(mcpservice.WithStaticToolsContainer(tools)),
    )

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

    log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

    // 5) Drop-in handler
    h, err := streaminghttp.New(
        ctx,
        publicEndpoint,
        host,
        server,
        authenticator,
        streaminghttp.WithServerName("My MCP Server"),
        streaminghttp.WithLogger(log),
        streaminghttp.WithAuthorizationServerDiscovery(issuer),
    )
    if err != nil {
        panic(err)
    }

    // 6) Serve
    http.ListenAndServe("127.0.0.1:8080", h)
}
```

See a runnable variant in `examples/readme/main.go`.

## Quick start (local dev, no IdP)

For quick experiments you can plug a minimal authenticator that accepts a fixed token. Do not use this in production.

```go
type staticTokenAuth struct{ token string }

type devUser struct{}
func (devUser) UserID() string       { return "dev" }
func (devUser) Claims(ref any) error { return nil }

func (a staticTokenAuth) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
    if tok == "Bearer "+a.token || tok == a.token { // accept raw or header value
        return devUser{}, nil
    }
    return nil, auth.ErrUnauthorized
}

// ... pass staticTokenAuth{token: "dev-token"} to streaminghttp.New(...)
// Then call your server with Authorization: Bearer dev-token
```

## Horizontal scale in one line (Redis)

```go
host, _ := redishost.New("127.0.0.1:6379", redishost.WithKeyPrefix("mcp:sessions:"))
defer host.Close()
```

- Ordered per-session message delivery with resume from `Last-Event-ID`.
- Cross-instance server-internal events for coordination when needed.
- Epoch-based invalidation helpers and revocation markers.

For single-process dev and tests, use the in-memory host:

```go
host := memoryhost.New()
```

## Authorization and discovery

When you enable discovery with `WithAuthorizationServerDiscovery(issuer)`, the handler:

- Validates tokens using JWKS from your issuer (via `auth.NewFromDiscovery`).
- Serves Protected Resource Metadata at `/.well-known/oauth-protected-resource/`.
- Mirrors Authorization Server Metadata at `/.well-known/oauth-authorization-server`.
- Responds to unauthorized requests with a standards-compliant `WWW-Authenticate` header that points at the resource metadata.

See the spec documents under `specs/` for details.

## Dynamic capabilities (and static containers)

Prefer dynamic? Implement per-session providers and callbacks:

```go
server := mcpservice.NewServer(
    // Dynamic tools
    mcpservice.WithToolsOptions(
        mcpservice.WithListTools(func(ctx context.Context, s sessions.Session, cur *string) (mcpservice.Page[mcp.Tool], error) {
            // e.g., query DB
            return mcpservice.NewPage([]mcp.Tool{{Name: "dbTool", InputSchema: mcp.ToolInputSchema{Type: "object"}}}), nil
        }),
        mcpservice.WithCallTool(func(ctx context.Context, s sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
            // route to your backend services
            return mcpservice.TextResult("ok"), nil
        }),
    ),
    // Dynamic resources
    mcpservice.WithResourcesOptions(
        mcpservice.WithListResources(func(ctx context.Context, s sessions.Session, cur *string) (mcpservice.Page[mcp.Resource], error) {
            return mcpservice.NewPage([]mcp.Resource{{URI: "res://1", Name: "R1"}}), nil
        }),
        mcpservice.WithReadResource(func(ctx context.Context, s sessions.Session, uri string) ([]mcp.ResourceContents, error) {
            return []mcp.ResourceContents{{URI: uri, MimeType: "text/plain", Text: "hello"}}, nil
        }),
    ),
)
```

Prefer static? Use the containers and still get listChanged and subscriptions:

```go
sr := mcpservice.NewStaticResources(nil, nil, nil)
rc := mcpservice.NewResourcesCapability(
    mcpservice.WithStaticResourceContainer(sr),
)

type HelloArgs struct {
    Name string `json:"name"`
}

st := mcpservice.NewStaticTools(
    mcpservice.NewTool[HelloArgs](
        "hello",
        func(ctx context.Context, _ sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[HelloArgs]) error {
            _ = w.AppendText("hi, " + r.Args().Name)
            return nil
        },
    ),
)
server := mcpservice.NewServer(
    mcpservice.WithResourcesCapability(rc),
    mcpservice.WithToolsOptions(mcpservice.WithStaticToolsContainer(st)),
)
```

## Examples and tests

- `examples/readme/` — end-to-end server that matches the Quick Start.
- `examples/echo/` — a simple echo tool server.
- `examples/resources_*` — static and filesystem-backed resources.
- See `tests/` for protocol-level and e2e tests that exercise sessions, listChanged, and multi-instance delivery.

## Compatibility

- MCP spec revision targeted: see `docs/mcp.md` (streaming HTTP transport; JSON-RPC batching is intentionally not used).
- Go version: see `go.mod` (currently `go 1.24`).

## Security notes

- Bearer token validation follows RFC 9068 with OIDC discovery (JWKS).
- Unauthorized requests receive standards-compliant `WWW-Authenticate` with pointers for discovery.
- The library is designed to avoid cross-user contamination across sessions and instances.

## License

MIT — see `LICENSE`.
