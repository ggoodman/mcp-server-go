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

## Ergonomic helpers (optional)

The core `mcp` package exposes only wire-level structs. For less boilerplate you can opt into the helper packages:

- `mcp/sampling` – build `CreateMessageRequest` values and message/content blocks.
- `mcp/elicitation` – build elicitation schemas via a small DSL and validate them.

You can ignore these packages entirely; they never wrap or hide protocol data structures.

### Sampling helpers

Raw (manual struct literals):

```go
req := &mcp.CreateMessageRequest{
    Messages: []mcp.SamplingMessage{
        {Role: mcp.RoleUser, Content: mcp.ContentBlock{Type: mcp.ContentTypeText, Text: "Translate to French: Hello"}},
    },
    SystemPrompt: "You translate only.",
    MaxTokens:    80,
}
```

With helpers:

```go
import "github.com/ggoodman/mcp-server-go/mcp/sampling"

req := sampling.NewCreateMessage(
    []mcp.SamplingMessage{
        sampling.UserText("Translate to French: Hello"),
    },
    sampling.WithSystemPrompt("You translate only."),
    sampling.WithMaxTokens(80),
)

if err := sampling.ValidateCreateMessage(req); err != nil { /* handle */ }
resp, _ := sp.CreateMessage(ctx, req)
```

### Elicitation (reflective API)

Elicitation lets the server request structured user input mid-flow. The
`sessions.ElicitationCapability` turns a struct definition into a restricted
schema (flat object; primitive properties) and decodes the response back into
the same struct.

```go
type LanguageChoice struct {
    Language string `json:"language" jsonschema:"description=Target language,minLength=1"`
}

el, ok := sess.GetElicitationCapability()
if ok {
    var lc LanguageChoice
    action, err := el.Elicit(ctx, &lc, sessions.WithMessage("Which language should I translate to?"))
    if err != nil { /* handle transport/validation error */ }
    if action == sessions.ElicitActionAccept {
        // use lc.Language
    }
}
```

Options:

* `sessions.WithMessage(string)` – prompt shown to the user.
* `sessions.WithStrictKeys()` – reject unknown keys (default: ignore extras).
* `sessions.WithRawCapture(&map[string]any{})` – access the raw response map.

Constraints (enforced server-side for now):

* No nested objects / arrays / oneOf / anyOf / refs.
* Enum only on string fields.
* Pointer fields are optional; non-pointer fields are required if present in the reflected schema's required set.
* Numeric min/max derived from jsonschema tags when supported by `invopop/jsonschema`.

### Design goals

1. Pure value builders: never introduce wrapper types.
2. Everything is additive; raw structs remain first-class.
3. Minimal API surface: helpers focus on reducing typos and repetition, not inventing new protocol concepts.
4. Easy escape hatch: you can intermix manual and helper-constructed values freely.

The earlier explicit request-building helper layer was removed in favor of the
single reflective API above. If you need to hand-author a schema, you can still
construct an `mcp.ElicitationSchema` directly and (in a future add-on) we may
expose a low-level call, but the reflective path is the primary ergonomics story.

## Structured logging (concise)

We use Go's standard `log/slog` with stable, dot-delimited event names. Correlation fields:

| Field       | When present                                   |
|-------------|-------------------------------------------------|
| `request_id`| Per inbound HTTP POST / GET stream / stdio req  |
| `session_id`| After session create / load                     |
| `user_id`   | After authentication succeeds                   |

Event naming: `<domain>.<action>.<state>` where `state ∈ {start, ok, fail, miss, denied, cancel, end}`.

| Transport | Event(s) | Meaning / Notes | Level |
|-----------|----------|-----------------|-------|
| HTTP | `http.post.start` | POST /mcp entry | info |
| HTTP | `session.create.ok` | New session created | info |
| HTTP | `session.load.ok` / `session.load.miss` | Load success / not found or unauthorized | info |
| HTTP | `protocol.version.mismatch` | Client vs session protocol mismatch | warn |
| HTTP | `rpc.inbound.start` / `rpc.inbound.ok` / `rpc.inbound.fail` | JSON-RPC (request with ID) lifecycle | info / error |
| HTTP | `notification.inbound.ok` / `notification.inbound.fail` | Notification handling result | info / warn |
| HTTP | `sse.stream.start` / `sse.stream.end` | GET /mcp SSE lifecycle | info |
| HTTP | `sse.message.deliver` | Outbound JSON-RPC message delivered on SSE | info |
| HTTP | `session.delete.ok` / `session.delete.fail` | DELETE /mcp lifecycle | info / error |
| HTTP | `auth.ok` / `auth.fail` | Authentication result (`fail` is expected denial) | info |
| stdio | `stdio.serve.start` | stdio loop start | info |
| stdio | `session.initialize.ok` | initialize handshake succeeded | info |
| stdio | `protocol.initialize.expected` | Non-initialize message before handshake | warn |
| stdio | `json.decode.fail` | Malformed inbound line | warn |
| stdio | `rpc.inbound.start` / `rpc.inbound.ok` / `rpc.inbound.fail` | Request lifecycle | info / error |
| stdio | `stdio.eof` | Graceful EOF shutdown | info |

Level guidelines: Info = normal lifecycle & auth denials; Warn = client/protocol misuse or malformed input; Error = internal failures / invariants.

Provide a custom logger with `streaminghttp.WithLogger(*slog.Logger)` (or analogous options elsewhere); only `Info/Warn/Error` are invoked.
