# Distributed Message Coordination (CRITICAL for Scaling)

## Status Quo

The current implementation is designed for single-instance deployment. Multiple TODO comments indicate awareness that distributed coordination is needed but not implemented.

**Code Location:**

- `handler.go:438` - "TODO: Dispatch the response to the communication mesh"
- `handler.go:405` - "TODO: Dispatch the notification to the 'right place'"
- `handler.go:910` - "TODO: Actually handle the response"
- Broker interfaces exist but no distributed coordination

**Current Flow:**

1. Client request lands on instance A
2. Instance A processes request and sends response via SSE
3. No mechanism for cross-instance communication
4. Server-initiated messages have no routing

**Critical Problems:**

- No way to route server-initiated requests to correct client instance
- No response correlation across instances
- No message broadcasting for notifications
- SSE connections are instance-local only

## The Gap

**Distributed System Requirements:**

- Request/response correlation across instances
- Server-initiated message routing (sampling, logging, notifications)
- Message broadcasting for session notifications
- Connection affinity or migration support

**Missing Architecture:**

- Message broker/coordination layer
- Instance-to-instance communication
- Session-to-instance mapping
- Message routing and delivery guarantees

## TODOs to Close Gap

### 1. Design Distributed Coordination Architecture

**Options to Evaluate:**

- [ ] Redis Streams + Pub/Sub for message routing
- [ ] NATS/NATS JetStream for message coordination
- [ ] Database-based coordination with polling
- [ ] Custom WebSocket mesh between instances

### 2. Implement Message Broker Interface

```go
type MessageBroker interface {
    // Route messages to specific sessions regardless of instance
    RouteToSession(ctx context.Context, sessionID string, message []byte) error

    // Subscribe to messages for sessions handled by this instance
    SubscribeToSessionMessages(ctx context.Context, handler MessageHandler) error

    // Broadcast notifications to all active sessions
    BroadcastNotification(ctx context.Context, notification []byte) error

    // Register this instance as handler for specific sessions
    RegisterSessionHandler(ctx context.Context, sessionID string, instanceID string) error
}
```

### 3. Add Instance Management

- [ ] Unique instance ID generation
- [ ] Instance registration and heartbeat
- [ ] Instance discovery and routing tables
- [ ] Instance failure detection and failover

### 4. Implement Request/Response Correlation

```go
type PendingRequest struct {
    RequestID   string
    SessionID   string
    InstanceID  string
    CreatedAt   time.Time
    ResponseCh  chan *jsonrpc.Response
}

type DistributedRequestTracker interface {
    TrackRequest(req PendingRequest) error
    RouteResponse(sessionID string, response *jsonrpc.Response) error
    CleanupExpiredRequests(ctx context.Context) error
}
```

### 5. Add Session-to-Instance Mapping

- [ ] Track which instance handles each active session
- [ ] Update mapping on connection establishment
- [ ] Clean up mapping on disconnection
- [ ] Handle instance failures and session migration

### 6. Implement Server-Initiated Message Routing

- [ ] Route sampling requests to correct client instance
- [ ] Route elicitation requests to correct client instance
- [ ] Route logging messages to correct client instance
- [ ] Handle progress notifications across instances

### 7. Add Message Broadcasting

- [ ] Broadcast tool list changes to all sessions
- [ ] Broadcast resource list changes to all sessions
- [ ] Broadcast prompt list changes to all sessions
- [ ] Handle subscription-based notifications

### 8. Update Handler for Distributed Operation

- [ ] Replace TODO comments with actual broker calls
- [ ] Add message routing in `handleResponse`
- [ ] Add notification dispatching in `handleNotification`
- [ ] Implement cross-instance session lookup

### 9. Add Connection Affinity Support

- [ ] Provide load balancer hints for session affinity
- [ ] Implement session migration for instance failures
- [ ] Handle connection handoff between instances
- [ ] Add graceful instance shutdown with session migration

### 10. Implement Distributed Session Management

```go
type DistributedSessionManager interface {
    SessionManager

    // Additional methods for distributed operation
    MigrateSession(ctx context.Context, sessionID string, targetInstance string) error
    GetSessionInstance(ctx context.Context, sessionID string) (string, error)
    HandleInstanceFailure(ctx context.Context, failedInstance string) error
}
```

**Priority:** CRITICAL - Blocking horizontal scaling
**Estimated Effort:** Very Large (15-20 days)
**Dependencies:** Message broker selection, distributed session storage
