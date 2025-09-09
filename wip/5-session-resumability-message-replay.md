# Session Resumability and Message Replay (HIGH)

## Status Quo

Sessions can be created and loaded, but there's no proper resumption mechanism for handling client reconnections or message replay based on `last-event-id`.

**Code Location:**

- `handler.go:515-570` - `handleGetMCP()` reads `last-event-id` header
- `handler.go:525-550` - `session.ConsumeMessages()` call with lastEventID
- Sessions interface supports message consumption but resumption logic is unclear

**Current Flow:**

1. Client disconnects and reconnects
2. Client sends `last-event-id` header with last received message ID
3. Server calls `session.ConsumeMessages(ctx, lastEventID, callback)`
4. Unclear what happens with message replay and ordering

**Problems:**

- No clear definition of what constitutes a resumable session
- No validation that lastEventID is valid for the session
- No guarantee of message ordering or completeness
- No handling of missed messages during disconnection

## The Gap

**Spec Requirement:**

- Clients must be able to resume sessions after disconnection
- Message replay must maintain ordering and completeness
- `last-event-id` must enable reliable message replay

**Missing Implementation:**

- Session persistence across disconnections
- Message ID generation and sequencing
- Reliable message replay from specific points
- Connection state management

## TODOs to Close Gap

### 1. Define Session Resumability Model

- [ ] Determine session lifetime and expiration rules
- [ ] Define what makes a session resumable vs requiring re-initialization
- [ ] Add session state persistence requirements

### 2. Implement Message Sequencing

```go
type MessageEnvelope struct {
    MessageID   string    // Globally unique, monotonic message ID
    SessionID   string    // Session this message belongs to
    Timestamp   time.Time // When message was created
    SequenceNum int64     // Monotonic sequence number within session
    Message     []byte    // JSON-RPC message content
}
```

### 3. Enhance Session Message Storage

- [ ] Ensure messages are stored with monotonic IDs
- [ ] Add message retention policies (time-based, count-based)
- [ ] Implement efficient message range queries
- [ ] Add message deduplication

### 4. Implement Reliable Message Replay

```go
func (s *Session) ReplayMessagesFrom(lastEventID string) ([]MessageEnvelope, error) {
    // Find starting point based on lastEventID
    // Return all messages since that point in order
    // Handle missing/invalid lastEventID gracefully
}
```

### 5. Add Connection State Management

- [ ] Track active connections per session
- [ ] Handle multiple concurrent connections to same session
- [ ] Implement connection cleanup on disconnect
- [ ] Add connection affinity hints for load balancers

### 6. Update Message Consumption Logic

- [ ] Validate lastEventID against session message history
- [ ] Return appropriate errors for invalid replay requests
- [ ] Ensure message replay doesn't duplicate in-flight messages
- [ ] Handle edge cases (empty lastEventID, too-old lastEventID)

### 7. Add Session Expiration and Cleanup

```go
type SessionManager interface {
    // Existing methods...

    ExtendSession(ctx context.Context, sessionID string) error
    ExpireSession(ctx context.Context, sessionID string) error
    CleanupExpiredSessions(ctx context.Context) error
}
```

### 8. Implement Message Ordering Guarantees

- [ ] Ensure messages within a session are totally ordered
- [ ] Handle distributed ordering in multi-instance deployments
- [ ] Add sequence number validation
- [ ] Detect and handle message gaps

### 9. Add Resumption Validation

- [ ] Verify session is in resumable state
- [ ] Check user authorization for session resumption
- [ ] Validate connection attempts against session metadata
- [ ] Handle session state conflicts

**Priority:** HIGH - Critical for production reliability
**Estimated Effort:** Large (7-10 days)
**Dependencies:** Message storage implementation, distributed coordination design
