# Contributing to mcp-streaming-http-go

Thanks for your interest in contributing! This guide explains the library's philosophy and the main development workflow.

## Philosophy

- Small, composable building blocks: the HTTP transport, capability surfaces, and session hosts are decoupled and testable.
- Batteries-included defaults: typed tools, static resources, Redis session host, and OIDC/JWT auth work out of the box.
- Safe by default: strict tool argument decoding (reject unknown fields) and explicit capability wiring.
- Production-friendly: supports horizontal scale, change notifications, pagination, and streamlined HTTP/SSE paths.

## Project structure (high level)

- `streaminghttp/` – HTTP handler for MCP streaming transport
- `mcpserver/` – server capability wiring (tools, resources, prompts, etc.)
- `mcp/` – protocol types and JSON-RPC message shapes
- `sessions/` – session hosts (`memoryhost`, `redishost`)
- `internal/` – helpers (JSON-RPC, JWT auth helpers, well-known metadata)
- `examples/` – runnable examples
- `tests/` – end-to-end tests

## Dev environment

- Go 1.24+
- Redis (if you want to run Redis-backed e2e tests locally)

## Common tasks

- Format: `go fmt ./...`
- Lint/typecheck quickly in-editor; or run `go vet ./...` if desired.
- Tidy deps: `go mod tidy`
- Run unit tests: `go test ./...`
- Run examples: see `examples/*`

## Typed tools

We use `github.com/invopop/jsonschema` to reflect JSON Schemas from Go types and down-convert them to MCP's simplified `ToolInputSchema`. We default to `additionalProperties=false` in schemas and DisallowUnknownFields in runtime decoding. Consumers can opt in to leniency with `WithToolAllowAdditionalProperties(true)`.

## Change notifications

Capabilities that support list-changed (like tools/resources) use a small in-process `ChangeNotifier` + `ChangeSubscriber` mechanism. Container types (`ToolsContainer`, `ResourcesContainer`) notify automatically on Replace/Add/Remove.

## Tests

- Unit tests live near the code
- End-to-end tests live under `tests/`
- Please ensure `go test ./...` passes before sending a PR

## Pull requests

- Keep changes focused and additive
- Include tests when changing behavior
- Update README when you add/modify public APIs

Thanks for contributing!
