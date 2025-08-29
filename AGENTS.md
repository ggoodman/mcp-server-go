The goal of this project is to provide a drop-in `http.Handler` for developers to mount to their server and get a horizontally-scalable solution for MCP's streaming HTTP Transport.

## Key specs

This library MUST adhere to the following specs.

- MCP Spec: `./docs/mcp.md`
  - We are targeting the streaming http transport.
- MCP Schema: `./docs/mcp-schema.ts`
- JSON-RPC Spec: `./docs/json-rpc.html`
  - We do NOT leverage json-rpc batching. It is forbidden in the streaming http transport.

## Code style

- Idiomatic go. Use the standard library when possible. Only fall back to top-tier packages when re-implementation would be overly complex or risky.
- Layered approach that supports drop-in scenarios but allows users to peel back the layers and adapt to more complex scenarios.
- Horizontally scalable. JSON-RPC requests, notifications and responses may land on any node and the library must support coordination in such an environment.
- Designed for production users with minimal internal library opinions
- Security is a first-class concern. We take no risks when it comes to cross-user contamination.

## Tool usage

1. NEVER run `go build` if a similar outcome can be achieved by using the "Go Please" tool suite. It is the language server and will give faster more easily-understood feedback.
2. AVOID running `go test` if the built-in test runner can achieve the same thing.
