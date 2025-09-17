# MCP capabilities parity and tools ergonomics plan

This plan tracks work to reach full capability coverage across transports and to redesign the tools authoring API for better ergonomics and progressive features (progress, streaming, schemas).

## Milestones

- [ ] Complete capabilities parity (server and client)

  - [ ] Completions capability (server-side)
    - [ ] Add mcpservice.CompletionsCapability interface: Complete(ctx, session, *mcp.CompleteRequest) (*mcp.CompleteResult, error)
    - [ ] Add GetCompletionsCapability to mcpservice.ServerCapabilities; plumb through mcpservice.NewServer options
    - [ ] Handle completion/complete in streaminghttp and stdio handlers
    - [ ] Add unit/e2e tests and an example tool using completions
  - [ ] Logging message notifications
    - [ ] Provide helper to emit notifications/message (LoggingMessageNotification) to the client
    - [ ] Add optional mcpservice LoggingMessages capability or a utility: mcpservice.NewSlogBridge(h slog.Handler) to forward logs
    - [ ] Advertise support in initialize when enabled; document usage and impact
  - [ ] Progress notifications for long-running requests
    - [ ] Define a transport-agnostic ProgressEmitter interface and context injection: mcpservice.Progress(ctx).Report(current, total)
    - [ ] Derive progress token from JSON-RPC request ID (string form) and emit notifications/progress
    - [ ] Implement emission paths in stdio and streaminghttp
    - [ ] Add tests for progress during tools/call and resources/read
  - [ ] Inbound request cancellation
    - [ ] Track in-flight server-handled requests by JSON-RPC ID and cancel their contexts when notifications/cancelled arrives from client
    - [ ] Implement in stdio and streaminghttp with minimal contention and safe teardown
    - [ ] Tests: cancellation of tools/call and resources/read mid-flight
  - [ ] Client-side capabilities parity (server calling client)
    - [ ] Verify sampling, roots (incl. listChanged), and elicitation already work over both transports
    - [ ] Add small tests for each path (server → client) to validate dispatcher wiring and backpressure

- [ ] Tools ergonomics redesign

  - [ ] Introduce ToolResponseWriter API (http.ResponseWriter-inspired)
    - [ ] Interface: methods to AppendText, AppendResourceLink, AppendEmbeddedResource, SetIsError, SetMeta(key, v), and SendProgress(current, total)
    - [ ] Provide a Result() method returning \*mcp.CallToolResult for non-streaming use
    - [ ] Make writer safe for concurrent use per call, respecting ctx cancellation
  - [ ] New Tool handler shape and adapters
    - [ ] Define type ToolFunc func(ctx context.Context, s sessions.Session, req mcp.CallToolRequestReceived, w ToolResponseWriter) error
    - [ ] Add adapters to reuse existing handlers: from func(ctx, s, A) (\*mcp.CallToolResult, error) and ToolHandler to the new shape
    - [ ] Update StaticTools to support both shapes seamlessly
  - [ ] Typed input and output schemas for tools (optional output)
    - [ ] Introduce NewTool2[A any, R any](name string, fn func(ctx, s, A) (R, error), opts ...ToolOption) StaticTool
    - [ ] Reflect JSON Schema for R via invopop/jsonschema
    - [ ] Surface output schemas in tools/list via ListToolsResult.\_meta.toolsOutputSchemas: map[string]jsonschema
    - [ ] Document as an experimental extension (non-breaking for clients)
  - [ ] Progress convenience helpers for tools
    - [ ] mcpservice.ProgressFrom(ctx).Start(total).Step(n).Done() utility wrapping notifications/progress

- [ ] Transport updates to support ergonomics

  - [ ] streaminghttp
    - [ ] Capture request ID and inject progress emitter into ctx for each call
    - [ ] Ensure SSE writer path can interleave progress events before final response
    - [ ] Implement inbound cancellation mapping to per-request contexts
  - [ ] stdio
    - [ ] Same as above: progress and cancellation mapping; ensure writeMux handles interleaving notifications safely

- [ ] API consistency and polish (breaking changes acceptable)

  - [ ] Align option names across capabilities (e.g., WithPageSize consistently)
  - [ ] Consistent listChanged/subscription exposure helpers
  - [ ] Normalize error shapes and mapping (invalid params vs internal), reuse mcpservice error types where helpful

- [ ] Documentation and examples

  - [ ] Update README and AGENTS.md with capability matrix and ergonomics examples
  - [ ] Add an examples/tools_progress/ demo showing progress + output schema
  - [ ] Add comments to mcpservice doc.go explaining new patterns

- [ ] Test coverage and quality gates
  - [ ] Add unit tests for new capability surfaces and adapters
  - [ ] Add e2e tests for streaminghttp and stdio parity, including progress and cancellation
  - [ ] Ensure go vet, staticcheck, and race detector are clean

## Notes / Decisions

- Progress token: use the JSON-RPC request ID string value as the ProgressNotification.progressToken; this avoids extending request params and keeps correlation robust.
- Output schema publication: use ListToolsResult.\_meta.toolsOutputSchemas to avoid changing the MCP Tool type; mark clearly as experimental and optional.
- Backward compatibility: maintain existing ToolOption and NewTool; provide adapters so existing code requires minimal or no changes.
- Cancellation: never preemptively terminate non-idempotent operations; best-effort cancel via context. Handlers should respect ctx and return promptly.
