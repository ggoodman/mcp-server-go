# Initialization Flow and Capability Negotiation (HIGH)

## Status Quo

The MCP initialization process is incomplete. While basic session creation works, proper capability negotiation and initialization state tracking are missing.

**Code Location:**

- `handler.go:601-660` - `handleSessionInitialization()`
- `handler.go:24-46` - `sessionMetadata` struct with placeholder capabilities
- `handler.go:407-410` - Placeholder handling for `initialized` notification

**Current Flow:**

1. Client sends `initialize` request
2. Server creates session with placeholder capability metadata
3. Server responds with initialize result
4. Client `initialized` notification is acknowledged but not tracked

**Problems:**

- Client capabilities are not properly extracted and stored
- No validation that server capabilities match what's advertised
- No tracking of initialization completion state
- Capability objects are always nil (placeholders)

## The Gap

**Spec Violation:**

- MCP Lifecycle spec requires proper capability negotiation
- Client capabilities must be respected throughout session
- `initialized` notification must be tracked for session state

**Missing Implementation:**

- Capability extraction from initialize request
- Capability validation and negotiation
- Initialization state management
- Proper capability-based feature gating

## TODOs to Close Gap

### 1. Implement Capability Extraction

```go
func extractClientCapabilities(initReq *mcp.InitializeRequest) (*ClientCapabilities, error) {
    // Extract and validate sampling, roots, elicitation capabilities
    // Return structured capability objects
}
```

### 2. Enhance Session Metadata

- [ ] Replace placeholder capabilities with actual implementations
- [ ] Store full client capability details in session
- [ ] Add initialization state tracking (initialized/not initialized)
- [ ] Add negotiated server capabilities

### 3. Update Session Interface

```go
type Session interface {
    // Existing methods...

    IsInitialized() bool
    MarkInitialized() error
    GetClientCapabilities() ClientCapabilities
    GetServerCapabilities() ServerCapabilities
}
```

### 4. Implement Capability Negotiation

- [ ] Validate client capabilities against server capabilities
- [ ] Generate appropriate server capability response
- [ ] Store negotiated capabilities in session
- [ ] Reject unsupported capability combinations

### 5. Track Initialization State

- [ ] Update `initialized` notification handler to mark session as initialized
- [ ] Reject non-initialize requests before initialization complete
- [ ] Add session state validation to request handlers

### 6. Add Capability-Based Feature Gating

- [ ] Check client sampling capability before sending sampling requests
- [ ] Check client roots capability before sending roots requests
- [ ] Check client elicitation capability before sending elicitation requests
- [ ] Return appropriate errors for unsupported operations

### 7. Update Server Capability Advertisement

- [ ] Generate server capabilities based on available hooks
- [ ] Include sub-capabilities (listChanged, subscribe) appropriately
- [ ] Handle experimental capabilities

### 8. Add Capability Validation

```go
func validateCapabilityCompatibility(client ClientCapabilities, server ServerCapabilities) error {
    // Validate that client/server capabilities are compatible
    // Return specific errors for incompatibilities
}
```

**Priority:** HIGH - Core to proper MCP session management
**Estimated Effort:** Large (5-7 days)
**Dependencies:** Session interface updates, hooks capability introspection
