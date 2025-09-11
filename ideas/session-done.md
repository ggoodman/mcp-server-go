# Session-scoped Done channel: notes

Goal: provide a simple, reliable signal for when a specific session instance (e.g., an active GET /mcp stream) is finished, so background goroutines can stop promptly without leaking.

## Motivating cases

- Server-side goroutines registered for per-session events (e.g., resource list_changed) need to stop when the session stream ends.
- Some work wants a lifetime bound to the live client connection, not just the broader user/session identity.
- Today we pass the request `ctx` into registration methods. This works but forces the handler to be the plumbing point for every registration.

## Options considered

1. Add `Session.Done() <-chan struct{}`

   - Pros:
     - Simple and familiar (like `context.Context.Done`).
     - Call sites can select on a well-known signal without being handed a separate context.
   - Cons:
     - Clarify semantics: is this the lifetime of the ephemeral connection (GET /mcp) or the logical session across multiple requests?
     - Requires the session implementation to surface a clean shutdown path for each live instance.

2. Add `Session.Context() context.Context`

   - Pros:
     - One-stop source of cancellation, deadlines, and values.
     - Mirrors how the rest of the code already uses contexts.
   - Cons:
     - Overlaps with the existing method parameters that already accept a `ctx`.
     - Ambiguity: which context is it (connection-level vs. session-level)?

3. Add manager-level lifecycle hooks (e.g., `RegisterSessionCleanup(sessionID, func())`)
   - Pros:
     - Centralized; good for cross-cutting cleanup.
   - Cons:
     - Less ergonomic for call sites that just want a channel or context to select on.

## Recommended shape

Keep request-bound cancellation for handler-plumbed wiring (what we do now), and add a targeted signal only if we observe multiple call sites needing it:

- `Session.Done() <-chan struct{}` that closes when the active GET /mcp for this `Session` instance ends.
- Document that this is instance-scoped, not a global lifetime for the logical session key. Separate instances (reconnects) have separate `Done` channels.

Rationale:

- Keeps the handler clean (less boilerplate passing `ctx` into every registration).
- Makes it easy to compose `select { case <-sess.Done(): ... }` across helpers without threading every context.
- Avoids API churn now; only add once we confirm >1 consumer benefits.

## Minimal API sketch

```
type Session interface {
    // ...existing methods...
    // Done is closed when this session instance (e.g., the current GET /mcp stream) ends.
    // It MUST return a non-nil channel. The channel is closed exactly once.
    Done() <-chan struct{}
}
```

SessionManager responsibilities:

- When producing a `Session` for GET /mcp, back it with a per-instance done channel.
- Close the channel when the stream ends or the subscription loop exits.

## Backwards compatibility

- Additive API, no breaking changes.
- No behavior changes unless libraries start selecting on `Done`.

## Why not do this right now?

- Current handler already passes `ctx` to registration points; that guarantees cleanup for the primary consumer.
- Introducing `Done()` adds a second lifecycle source and needs careful documentation.
- Let’s revisit once we add a second or third consumer that benefits from avoiding handler-plumbed contexts.
