# Workspace FS example

This example shows a fully-functional local MCP server that:

- Exposes a configurable slice of the host filesystem as MCP resources
- Provides a minimal, practical set of filesystem tools
- Returns resource references (links and embedded contents) from tool calls

It’s designed to be a drop-in, local “developer workspace” server that a host program can connect to and use inside a sandboxed directory.

## What it exposes

- Resources: backed by the provided directory using `FSResources`
  - Lists files under the root
  - Reads file contents with MIME type detection
  - Emits list-changed and per-file updated notifications when possible
- Tools: a small, useful set to act like a developer inside the workspace
  - `fs.read`: Read a file by `uri` or `path`; returns EmbeddedResource
  - `fs.write`: Write a file; returns a ResourceLink and EmbeddedResource
  - `fs.append`: Append to a file; returns a ResourceLink and EmbeddedResource
  - `fs.move`: Move/rename a file; returns a ResourceLink
  - `fs.delete`: Delete a file; returns a short text confirmation

Example tool results include resource references so the client can keep working with the same files (open, display, read again, etc.).

## Using it from Go

Create a server bound to some folder:

```go
srv := workspace_fs.New("/path/to/workspace")
```

Mount it on the streaming HTTP handler (for local development you can use memory sessions and skip real auth by providing a stub):

```go
h, _ := streaminghttp.New(ctx, publicURL, memoryhost.New(), srv, noAuth,
    streaminghttp.WithServerName("workspace"),
  streaminghttp.WithSecurityConfig(auth.SecurityConfig{Issuer: "http://127.0.0.1:0", Audiences: []string{"example"}, JWKSURL: "http://127.0.0.1/.well-known/jwks.json", Advertise: true}),
)
http.Handle("/", h)
```

Clients connect using the MCP Streamable HTTP transport.

## Why this is powerful

- One package wires resources, tools, paging, and notifications in an idiomatic Go style
- Sandboxed by default: all operations are constrained to the configured root
- Resource references in tool results make downstream UX simpler and safer
- The same building blocks scale up later to distributed hosts and sessions

## Tests

- Library-level test: `TestLib_WorkspaceFS_ListAndTools` exercises list/read and tool calls directly against the server capabilities
- End-to-end test: `TestExamples_WorkspaceFS_E2E` spins up the streaming HTTP handler and an MCP client to verify the flow

Run tests as usual (the repo uses the Go language server for dev loops, but standard `go test` works too):

```sh
go test ./...
```
