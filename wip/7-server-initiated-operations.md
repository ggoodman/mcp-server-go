# Server-Initiated Operations (MEDIUM)

## Status Quo

The current implementation only handles client-initiated requests. Server-initiated operations (sampling, elicitation, roots requests) are not implemented, though the interfaces exist in the hooks package.

**Code Location:**

- `hooks/` package defines interfaces for server-initiated operations
- `handler.go` has no implementation for sending server-initiated requests
- No mechanisms for handling server-initiated request/response flows

**Current Capabilities:**

- Client can call server tools, read resources, get prompts
- Server cannot initiate sampling, elicitation, or roots requests
- No implementation of progress notifications
- No logging message distribution

## The Gap

**MCP Spec Requirements:**

- Servers must be able to request LLM sampling through clients
- Servers must be able to request user elicitation through clients
- Servers must be able to request filesystem roots from clients
- Servers must be able to send progress notifications
- Servers must be able to send logging messages

**Missing Implementation:**

- No server-to-client request initiation
- No response handling for server-initiated requests
- No progress notification system
- No client capability validation for server operations

## TODOs to Close Gap

### 1. Implement Server-Initiated Request Infrastructure

```go
type ServerRequestSender interface {
    SendSamplingRequest(ctx context.Context, session Session, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error)
    SendElicitRequest(ctx context.Context, session Session, req *mcp.ElicitRequest) (*mcp.ElicitResult, error)
    SendRootsListRequest(ctx context.Context, session Session) (*mcp.ListRootsResult, error)
    SendProgressNotification(ctx context.Context, session Session, notification *mcp.ProgressNotification) error
    SendLoggingMessage(ctx context.Context, session Session, message *mcp.LoggingMessageNotification) error
}
```

### 2. Add Request ID Management

- [ ] Generate unique request IDs for server-initiated requests
- [ ] Track pending server requests with timeouts
- [ ] Correlate responses to original requests
- [ ] Handle request cancellation and cleanup

### 3. Implement Sampling Capability

- [ ] Check client sampling capability before sending requests
- [ ] Implement sampling request generation and sending
- [ ] Handle sampling responses and errors
- [ ] Add sampling request timeout and retry logic

### 4. Implement Elicitation Capability

- [ ] Check client elicitation capability before sending requests
- [ ] Implement elicitation request generation and sending
- [ ] Handle elicitation responses and user input
- [ ] Add elicitation request validation

### 5. Implement Roots Capability

- [ ] Check client roots capability before sending requests
- [ ] Implement roots list request generation and sending
- [ ] Handle roots responses and filesystem information
- [ ] Add roots change notification support

### 6. Add Progress Notification System

```go
type ProgressTracker interface {
    StartProgress(ctx context.Context, token string, total int) error
    UpdateProgress(ctx context.Context, token string, current int, message string) error
    CompleteProgress(ctx context.Context, token string) error
}
```

### 7. Implement Logging Distribution

- [ ] Add logging level management per session
- [ ] Implement logging message queuing and delivery
- [ ] Handle logging message formatting and filtering
- [ ] Add logging message rate limiting

### 8. Update Session Interface for Server Operations

```go
type Session interface {
    // Existing methods...

    SendRequest(ctx context.Context, request *jsonrpc.Request) (*jsonrpc.Response, error)
    SendNotification(ctx context.Context, notification *jsonrpc.Request) error
    GetClientCapabilities() ClientCapabilities
}
```

### 9. Add Request/Response Routing

- [ ] Route server-initiated requests through message broker
- [ ] Handle responses from any instance back to originating server
- [ ] Implement request timeout and error handling
- [ ] Add request correlation and cleanup

### 10. Implement Server Request Handlers in Hook Interface

```go
// Update hooks to support server-initiated operations
type Hooks interface {
    // Existing methods...

    RequestSampling(ctx context.Context, session Session, prompt []mcp.PromptMessage) (*mcp.SamplingMessage, error)
    RequestElicitation(ctx context.Context, session Session, schema map[string]interface{}) (map[string]interface{}, error)
    RequestRoots(ctx context.Context, session Session) ([]mcp.Root, error)
    SendProgress(ctx context.Context, session Session, token string, progress int, total int, message string) error
    SendLogMessage(ctx context.Context, session Session, level mcp.LoggingLevel, message string, data interface{}) error
}
```

### 11. Add Client Capability Validation

- [ ] Validate client supports sampling before sending sampling requests
- [ ] Validate client supports elicitation before sending elicitation requests
- [ ] Validate client supports roots before sending roots requests
- [ ] Return appropriate errors for unsupported operations

### 12. Handle Server-Initiated Request Failures

- [ ] Implement retry logic for failed server requests
- [ ] Add circuit breaker for repeatedly failing clients
- [ ] Log server request failures appropriately
- [ ] Handle client disconnection during pending requests

**Priority:** MEDIUM - Important for full MCP functionality but not blocking
**Estimated Effort:** Large (8-12 days)
**Dependencies:** Distributed message coordination, session capability negotiation
