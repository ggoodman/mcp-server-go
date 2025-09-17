# Changelog

## Unreleased

### Added: stdio transport (experimental)

Introduce a new `stdio` transport package that enables running an MCP server over standard input/output. This is ideal for embedding servers as subprocesses or for local development when HTTP transports are unnecessary.

Highlights

- Single-connection transport reading JSON-RPC messages from `stdin` and writing responses to `stdout`.
- Works with existing `mcpservice.ServerCapabilities` – no behavioral changes to your server code.
- Defaults:
  - Input: `os.Stdin`
  - Output: `os.Stdout`
  - User identity: current OS user (username or UID)
- Options pattern allows overrides: custom `io.Reader`/`io.Writer`, logger, and user provider.

Usage

```go
srv := mcpservice.NewServer(
    mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "my-stdio-server", Version: "0.1.0"}),
    // ... add tools/resources/prompts
)

h := stdio.NewHandler(srv)
if err := h.Serve(context.Background()); err != nil {
    log.Fatal(err)
}
```

Customizing IO and Identity

```go
bufIn := bytes.NewBuffer(nil)
bufOut := &bytes.Buffer{}
h := stdio.NewHandler(srv,
    stdio.WithIO(bufIn, bufOut),
    stdio.WithUserProvider(MyUserProvider{}),
)
```

Notes

- The `stdio` transport intentionally skips HTTP auth and session headers. It identifies the caller as the current OS user by default. If you need a different identity model (e.g., per-pipe identity), supply a custom `UserProvider`.
- Lifecycle follows MCP basics: `initialize` -> `initialized` -> normal operation -> shutdown on EOF or context cancel.
- The API is stable; the internal loop implementation will land in a follow-up commit and adhere to the MCP stdio framing rules.

Security

- Suitable for local/embedded usage. For networked scenarios or multi-tenant environments, use the `streaminghttp` transport with proper authorization and session management.
