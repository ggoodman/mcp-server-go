<div align="center">

# mcp-server-go

<strong>Build Model Context Protocol servers that scale from a 20‑line stdio prototype to a horizontally scaled, OIDC‑protected streaming HTTP deployment — without rewriting business logic.</strong>

<p>
	<a href="https://pkg.go.dev/github.com/ggoodman/mcp-server-go"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/ggoodman/mcp-server-go.svg" /></a>
	<a href="https://goreportcard.com/report/github.com/ggoodman/mcp-server-go"><img alt="Go Report Card" src="https://goreportcard.com/badge/github.com/ggoodman/mcp-server-go" /></a>
	<a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-green.svg" /></a>
	<a href="https://github.com/ggoodman/mcp-server-go/actions/workflows/ci-test.yml"><img alt="CI - Test" src="https://github.com/ggoodman/mcp-server-go/actions/workflows/ci-test.yml/badge.svg?branch=main" /></a>
</p>

</div>

---

## Install / Import

Go module: `github.com/ggoodman/mcp-server-go`

Add to your project:

```bash
go get github.com/ggoodman/mcp-server-go@latest
```

Browse documentation: https://pkg.go.dev/github.com/ggoodman/mcp-server-go

Minimum Go version: as declared in `go.mod` (currently 1.24). The module follows standard Go module semantic import versioning (no v2 path yet).

---

## TL;DR Quickstart (stdio CLI)

Below is a tiny CLI MCP server exposing a single vibe-checking tool. The tool demonstrates using sampling and elicitation. It also shows how a response can be constructed through a `http.ResponseWriter`-like API.

```go
package main

import (
	"context"
	"log"

	"github.com/ggoodman/mcp-server-go/elicitation"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcp/sampling"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/stdio"
)

// Minimal args: none needed to start the interaction.
type VibeArgs struct{}

func main() {
	tool := mcpservice.NewTool("vibe_check", func(ctx context.Context, s sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[VibeArgs]) error {
		_ = w.AppendBlocks(mcp.ContentBlock{Type: mcp.ContentTypeText, Text: "Initiating vibe check…"})

		el, ok := s.GetElicitationCapability()
		if !ok {
			w.AppendText("Not feeling the vibes on this client.")
			w.SetError(true)
			return nil
		}

		type Phrase struct {
			Phrase string `json:"phrase" jsonschema:"minLength=3,description=Your current vibe in a few words"`
		}
		var p Phrase
		dec, err := elicitation.BindStruct(&p)
		if err != nil {
			return err
		}

		phraseText := "not feeling it"

		action, err := el.Elicit(ctx, "What's the vibe?", dec)
		if err == nil && action == sessions.ElicitActionAccept {
			phraseText = p.Phrase
		}

		samp, ok := s.GetSamplingCapability()
		if !ok {
			w.AppendText("can't feel the vibes")
			w.SetError(true)
			return nil
		}

		// Sample host LLM for a single whimsical word.
		res, err := samp.CreateMessage(ctx, sampling.NewCreateMessage([]mcp.SamplingMessage{{
			Role:    mcp.RoleUser,
			Content: mcp.ContentBlock{Type: mcp.ContentTypeText, Text: phraseText},
		}}, sampling.WithSystemPrompt("Respond with short phrase, capturing the emotional vibe of the submitted message with a touch of whimsy."),
			sampling.WithMaxTokens(40),
		))
		if err != nil {
			return err
		}

		w.AppendBlocks(res.Content)

		return nil
	}, mcpservice.WithToolDescription("Elicits a phrase then performs a playful LLM-powered vibe check."))

	tools := mcpservice.NewToolsContainer(tool)
	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcpservice.StaticServerInfo("vibe-check-demo", "0.0.1")),
		mcpservice.WithToolsCapability(tools),
	)

	h := stdio.NewHandler(server)
	if err := h.Serve(context.Background()); err != nil {
		log.Fatal(err)
	}
}
```

Upgrade path: swap the transport + host; your `server` stays the same.

```go
// (Sketch – not a full program)
host := redishost.New(redisClient) // or your implementation of sessions.SessionHost
authn, _ := auth.NewFromDiscovery(ctx, issuerURL, auth.WithExpectedAudience(publicURL))
httpHandler, _ := streaminghttp.New(ctx, publicURL, host, server, authn,
    streaminghttp.WithAuthorizationServerDiscovery(issuerURL),
    streaminghttp.WithServerName("reverse-prod"),
)
http.Handle("/mcp", httpHandler)
```

---

## Capability Model (Server Side)

At initialization the client sends its `ClientCapabilities`; the server responds with `ServerCapabilities`. Each negotiated capability unlocks a method set (see `mcp/messages.go`). Server implementations choose between:

1. **Containers (static sets)** – simple, mutation helpers, built‑in pagination and change notifications.
2. **Provider funcs (dynamic)** – per session logic (return (cap, ok, err)).

Each capability is configured by a single `With*Capability` option that accepts a provider:

* Pass a container (e.g. `NewToolsContainer`) directly – containers self‑implement the provider.
* Or pass an `XCapabilityProviderFunc` for per‑session logic.

## Authorization and discovery

When you enable discovery with `WithAuthorizationServerDiscovery(issuer)`, the handler:

- Validates tokens using JWKS from your issuer (via `auth.NewFromDiscovery`).
- Serves Protected Resource Metadata at `/.well-known/oauth-protected-resource/`.
- Mirrors Authorization Server Metadata at `/.well-known/oauth-authorization-server`.
- Responds to unauthorized requests with a standards-compliant `WWW-Authenticate` header that points at the resource metadata.

See the spec documents under `specs/` for details.

## Capability providers (static & dynamic)

Every capability is configured via exactly one option: `WithResourcesCapability`, `WithToolsCapability`, `WithPromptsCapability`, `WithLoggingCapability`, etc. Each option takes a provider – something implementing the corresponding `XCapabilityProvider` interface.

Three ergonomic patterns:

1. Static constant: use the `StaticX` helpers, e.g. `WithProtocolVersion(StaticProtocolVersion("2025-06-18"))` or `WithServerInfo(StaticServerInfo("name", "version", WithServerInfoTitle("Nice Title")))`.
2. Self-providing container: pass a container directly. `NewToolsContainer(...)` and `NewResourcesContainer(...)` implement both the capability and its provider; just do `WithToolsCapability(tools)`.
3. Per-session dynamic logic: provide an `XCapabilityProviderFunc` closure. It receives context + session and can return a tailored capability (or `ok=false` to omit it for that session).

Return `(value, ok=true, nil)` to advertise a capability even if its list is empty. Return `ok=false` to *omit* that capability altogether.

ListChanged notifications are emitted when underlying containers signal a change (e.g. `Replace` / `ReplaceResources`). This works uniformly for static containers and dynamic implementations.

## Dynamic capabilities (and static containers)

Prefer dynamic? Provide an `XCapabilityProviderFunc` closure when constructing the server.

---

### Humorous Elicitation Mini-Example

Collect user input mid-tool without designing a new schema manually. Below a tool elicits a favorite snack then responds with two content blocks.

```go
tool := mcpservice.NewTool[struct{}]("snack_oracle", func(ctx context.Context, s sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[struct{}]) error {
	if el, ok := s.GetElicitationCapability(); ok {
		type Snack struct { Name string `json:"name" jsonschema:"minLength=2,description=Favorite snack"` }
		var sn Snack
		dec := elicitation.BindStruct(&sn)
		action, err := el.Elicit(ctx, "What snack fuels your coding?", dec)
		if err != nil || action != sessions.ElicitActionAccept { return nil } // minimal handling
		_ = w.AppendText("Crunching the data...")
		_ = w.AppendBlocks(mcp.ContentBlock{Type: mcp.ContentTypeText, Text: "Consensus: " + sn.Name + " increases bug-free LOC by 0%. Delicious anyway."})
		return nil
	}
	_ = w.AppendText("Client can't elicit. Falling back to generic advice: hydrate.")
	return nil
}, mcpservice.WithToolDescription("Politely asks for your favorite snack."))
```

Add it next to your other tools in a container; structured schema is auto-reflected.

---

## Session-Level Capabilities (Client → Server negotiated)

After initialization you work with a `sessions.Session` value that exposes optional per-session capabilities negotiated during the handshake. These sit alongside the server capability interfaces and let you bridge user workflows that require *client cooperation* (sampling, roots, elicitation) while keeping domain logic cohesive.

`Session` (excerpt):

```go
type Session interface {
	SessionID() string
	UserID() string
	ProtocolVersion() string

	GetSamplingCapability() (cap SamplingCapability, ok bool)
	GetRootsCapability() (cap RootsCapability, ok bool)
	GetElicitationCapability() (cap ElicitationCapability, ok bool)
}
```

Pattern: fetch capability → if present call → honor context cancellation.

### SamplingCapability

Use when you need the client (or host application) to produce a model-generated message given prior exchange context.

```go
if samp, ok := sess.GetSamplingCapability(); ok {
	req := sampling.NewCreateMessage([]mcp.SamplingMessage{
		{Role: mcp.RoleUser, Content: mcp.ContentBlock{Type: mcp.ContentTypeText, Text: "Summarize current plan"}},
	}, sampling.WithSystemPrompt("Be concise"), sampling.WithMaxTokens(256))
	if err := sampling.ValidateCreateMessage(req); err != nil { return err }
	res, err := samp.CreateMessage(ctx, req)
	if err != nil { return err }
	log.Printf("model=%s reply=%s", res.Model, res.Content.Text)
}
```

Guidelines:
* Keep requests minimal; let client/host append its own context.
* Validate with `sampling.ValidateCreateMessage` in development to catch mistakes early.
* Treat absence (`ok == false`) as “feature not negotiated” and degrade gracefully.

### RootsCapability

Represents a logical workspace hierarchy (e.g. project roots / mount points) the client can surface. Use for enumerating base folders before listing resources from another subsystem or external index.

```go
if rootsCap, ok := sess.GetRootsCapability(); ok {
	roots, err := rootsCap.ListRoots(ctx)
	if err != nil { return err }
	for _, r := range roots.Roots {
		log.Printf("root: %s (%s)", r.URI, r.Name)
	}
	// Optional: register change listener if supported
	_, _ = rootsCap.RegisterRootsListChangedListener(ctx, func(lctx context.Context) error {
		updated, err := rootsCap.ListRoots(lctx)
		if err == nil {
			log.Printf("roots changed: %d", len(updated.Roots))
		}
		return nil // keep listening
	})
}
```

Listener semantics: returning an error stops delivery; returning nil keeps the registration active (implementation may re-call on change events).

### ElicitationCapability

Collect structured user input (with validation) mid-flight without inventing ad-hoc tool schemas for every prompt.

```go
if el, ok := sess.GetElicitationCapability(); ok {
	type Input struct {
		Name string `json:"name" jsonschema:"minLength=1,description=Your name"`
	}
	var in Input
	// Bind struct into a decoder
	dec := elicitation.BindStruct(&in) // (builder constructs schema + decoder)
	action, err := el.Elicit(ctx, "Who are you?", dec, sessions.WithStrictKeys())
	if err != nil { return err }
	if action != sessions.ElicitActionAccept { /* user declined or cancelled */ return nil }
	log.Printf("user identified as %s", in.Name)
}
```

Options:
* `sessions.WithStrictKeys()` – reject unexpected properties.
* `sessions.WithRawCapture(&m)` – capture raw map for auditing or secondary parsing.

Best practices:
* Fail fast on schema reflection errors before issuing Elicit calls.
* Keep elicitation schemas shallow (current implementation rejects nested objects/arrays for simplicity/performance).
* Treat non-accept actions as soft negative signals — do not log as errors.

---

### Resources Container Example

```go
res := []mcp.Resource{{URI: "file:///README.md", Name: "readme"}}
contents := map[string][]mcp.ResourceContents{
	"file:///README.md": {{Contents: []mcp.ContentBlock{{Type: "text", Text: "hello"}}}},
}
rc := mcpservice.NewResourcesContainer(res, nil, contents)
server := mcpservice.NewServer(mcpservice.WithResourcesCapability(rc))
```

Resource subscriptions & change notifications are bridged automatically when the transport and client both advertise support.

### Mixing Static + Dynamic

You can combine containers for straightforward domains and dynamic providers where session‑aware filtering or expensive lazy construction is needed. Each `With*Capability` sets a static implementation; the corresponding `With*Provider` overrides it if both are supplied.

---

## Client Capabilities (What the Server Receives)

Client initialization request (`initialize`) includes:

```jsonc
{
	"protocolVersion": "2025-06-18", // example
	"capabilities": { /* client capability bits */ },
	"clientInfo": {"name":"my-client","version":"1.2.3"}
}
```

Server decides preferred protocol (`GetPreferredProtocolVersion`) and optionally returns human instructions. The negotiated server capabilities then govern which MCP methods are valid for the session. If you omit a capability (e.g. tools) the transport simply rejects those method calls up front — your code never sees them.

### Progress & Cancellation

Long running tool calls can emit `notifications/progress` (server -> client) and clients can cancel by sending `notifications/cancelled`. The engine mediates this for you; just pay attention to `ctx.Done()` inside handlers.

### Sampling & Elicitation Helpers

Package `mcp/sampling` provides ergonomic helpers for building sampling messages; `elicitation` provides a builder for collecting structured values from the client. They layer cleanly atop the same session and transport plumbing.

---

## Pluggable Session Storage (`SessionHost`)

`SessionHost` is the persistence + messaging abstraction the engine uses (see `sessions/host.go`). It unifies:

1. Ordered, resumable per‑session outbound stream (`PublishSession` / `SubscribeSession`).
2. Internal pub/sub topics (`PublishEvent` / `SubscribeEvents`) for cross‑node coordination.
3. Session metadata lifecycle (create / mutate / touch / delete).
4. Per‑session bounded key/value store.

Reference implementations:

| Package | Characteristics | When to Use |
|---------|-----------------|-------------|
| `sessions/memoryhost` | In‑memory, ephemeral, zero deps | Tests, prototyping, stdio, single-node |
| `sessions/redishost`  | Durable metadata + streams via Redis | Streaming HTTP, multi-node, need fan-out |

Implement your own by satisfying `sessions.SessionHost`. Focus areas:

* At‑least‑once delivery for session stream (client handles idempotency).
* Minimal latency for publish + subscribe paths.
* Safe concurrent access (multiple engines / transports may share host).

---

## High-Level Architecture & Data Flow

```mermaid
flowchart LR
	Client -- JSON-RPC (stdio / SSE over HTTP) --> Transport
	subgraph ServerProcess
		Transport --> Engine
		Engine --> Capabilities[Server Capabilities Impl]
		Engine --> Host[(SessionHost)]
		Capabilities <--> Host
	end

	Host -. horizontal scale .- HostReplica[(SessionHost on another node)]
```

1. Transport (stdio or streaming HTTP) performs framing + auth (HTTP mode) and session header validation.
2. Engine owns the session handshake, capability discovery, dispatch, fan‑out of subscription notifications, progress + cancellation bridging.
3. Capabilities implement domain logic (tools, resources, prompts…). They stay ignorant of transport and persistence details.
4. `SessionHost` provides ordered event streams + metadata durability; swapping it changes scaling characteristics without touching capability logic.

Key property: A single logical pipeline of JSON‑RPC messages flows through transport → engine → capability and back. Horizontal scaling comes from making the host and engine stateless aside from what is persisted in `SessionHost`.

---

## Scaling Path

| Phase | Transport | Host | Auth | Notes |
|-------|-----------|------|------|-------|
| Proto | `stdio` | `memoryhost` | none | 0 infra, fastest iteration |
| Pilot | streaming HTTP | `memoryhost` (single pod) | OIDC (optional) | Introduce real clients, measure |
| Scale | streaming HTTP (multi) | `redishost` | OIDC + scopes | Add observability, autoscale |
| Custom | streaming HTTP | your host | OIDC + custom claims | Special persistence / tenancy |

---

## Design Decisions (Condensed)

* **Capability interfaces** isolate protocol surface; containers reduce boilerplate for static cases.
* **Functional options** allow mixing static and dynamic providers without config structs.
* **At‑least‑once delivery** chosen over exactly-once to avoid distributed consensus complexity; handlers must be idempotent.
* **Separation of concerns:** transport handles framing & auth; engine handles protocol flow; capabilities handle business data; host handles durability & fan-out.

---

## Future Directions (Indicative, Not Promises)

* Structured logging adapter examples.
* Additional host implementations (SQL / S3 hybrid, memory+lru shard, etc.).
* Metrics hooks (per method latency, subscription churn).
* Richer prompt templating helpers.

Contributions welcome — see `CONTRIBUTING.md`.

---

## FAQ (Early)

**Q: Do I need Redis to start?**  No — stdio + memory host gets you running instantly.

**Q: How do I add authentication?** Use `auth.NewFromDiscovery` with `WithExpectedAudience(publicURL)` then pass the authenticator to `streaminghttp.New`.

**Q: How do I emit progress?** The tool writer returned by `NewTool*` supports `_ = w.Progress(fraction)`. Honor context cancellation to be well-behaved.

**Q: Are method names stable?** Protocol revision tags define stability; this SDK tracks the spec revision you compile against.

---

## License

MIT – see `LICENSE`.

---

Feedback, missing ergonomics, or sharp edges? Open an issue with a focused description; API changes are still on the table before 1.0.

