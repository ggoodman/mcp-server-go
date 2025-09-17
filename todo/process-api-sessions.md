# Sessions Process API (Plan)

Objective: enable full MCP client-side capabilities (sampling, roots, elicitation) over stdio by introducing a robust outbound JSON-RPC pipeline and wiring it into `sessions.Session` for the stdio transport.

## Scope

- Outbound JSON-RPC core for server-initiated requests
- Cancellation and progress routing for outbound calls
- Session client capability adapters using the dispatcher
- Minimal server-side parity improvements that unblock protocol expectations
- Tests that stress concurrency, ordering, and lifecycle

## Design

### 1) Outbound JSON-RPC Dispatcher

Contract:

- Call(ctx, method, params) -> (result json.RawMessage, err)
- SendNotification(method, params) error (for `notifications/cancelled`, optional now)
- OnResponse(resp \*jsonrpc.Response) to resolve pending calls
- OnNotification(any jsonrpc.AnyMessage) for progress/cancel handling
- Close(err) to fail all pending with the given error

Details:

- ID generation: monotonic uint64 increment, wrapped by `jsonrpc.NewRequestID`.
- Pending: map[*jsonrpc.RequestID]pending; pending holds a result channel and optional progress sink; guarded by mutex.
- Write path: reuse `writeMux` to serialize writes; marshal a `jsonrpc.Request` with method/params/id.
- Read path integration: when `AnyMessage.Type()=="response"`, call dispatcher.OnResponse().
- Context cancellation: if ctx.Done() before response, fire `notifications/cancelled` with the same id (best-effort), remove pending, return ctx.Err().
- Shutdown: Close() cancels all pending with a terminal error (e.g., io.EOF or context.Canceled) and prevents new calls.

Edge cases:

- Out-of-order responses: resolved by pending map lookup.
- Stray responses: ignore (no pending).
- Duplicate IDs: impossible via local generator; guard anyway.

### 2) Progress and Cancellation Notifications

- Progress: observe `notifications/progress` carrying `progressToken`. Initially, we’ll parse and drop unless a future API wires a callback. Keep a token->callback map for forward compatibility; no-op if none.
- Cancelled: observe `notifications/cancelled` referencing a requestId. If it matches a pending outbound call, cancel and complete it with a clear error (e.g., ErrRemoteCancelled).

### 3) stdio Session Capability Adapters

Attach to `stdioSession`:

- SamplingCapability
  - CreateMessage(ctx, req) -> outbound `sampling/createMessage`; return decoded `mcp.CreateMessageResult`.
  - Pass through ctx cancel; no-op on progress for now.
- RootsCapability
  - ListRoots(ctx) -> outbound `roots/list`.
  - RegisterRootsListChangedListener(ctx, fn): store listener; enable after client `notifications/initialized`; on `notifications/roots/list_changed` run listeners (ordered, best-effort).
- ElicitationCapability
  - Elicit(ctx, req) -> outbound `elicitation/create`.

Sequencing:

- Server should only initiate outbound calls after client `notifications/initialized` observed, mirroring current listChanged gating.

### 4) Server-side Parity (small)

- resources/templates/list: implement in `handleRequest` using `mcpservice.ResourcesCapability` if available.
- resources/updated: if the resources container exposes granular update hooks, register alongside listChanged and emit `notifications/resources/updated`.
- logging/setLevel: optional; wire to a hook to update logger level.

### 5) Testing Strategy

Dispatcher unit tests (table-driven):

- Happy path: two concurrent outbound calls, responses arrive out of order.
- Cancellation: ctx canceled before response sends `notifications/cancelled` and returns context error.
- Remote cancel: incoming `notifications/cancelled` aborts matching call.
- Shutdown: Close() cancels all pending and prevents new calls.

Session adapter integration tests (stdio):

- sampling/createMessage: client sends response; also test cancellation.
- roots/list: returns roots; sending `notifications/roots/list_changed` triggers registered listener.
- elicitation/create: happy path.

Transport sequencing:

- Ensure no server-initiated calls until client `notifications/initialized`.

### 6) Open Questions

- Progress surfacing: do we want to extend `sessions.SamplingCapability` to support progress callbacks, or keep the server blind to progress for now?
- Logging setLevel: should we expose a provider to map levels to the underlying slog instance, or keep a no-op default?
- Backpressure: if a client floods progress notifications, we currently drop them; acceptable for now.

## Incremental Plan

1. Implement dispatcher and tests (no capability wiring yet).
2. Wire dispatcher into stdio handler (route responses/notifications) and expose on `stdioSession`.
3. Implement SamplingCapability via dispatcher (+ tests).
4. Implement RootsCapability (+ notifications/roots/list_changed) (+ tests).
5. Implement ElicitationCapability (+ tests).
6. Add resources/templates/list and resources/updated forwarding (+ tests).

This gives us a hardened base to support remaining features without reworking IO or lifecycles later.
