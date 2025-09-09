# Protocol Version Negotiation (HIGH)

## Status Quo

The MCP initialization flow does not properly implement protocol version negotiation as required by the MCP Lifecycle spec. Version checking is incomplete and the required `MCP-Protocol-Version` HTTP header is not enforced.

**Code Location:**

- `handler.go:601-660` - `handleSessionInitialization()` function
- Missing version negotiation logic in initialize request/response

**Current Flow:**

1. Client sends `initialize` request with `protocolVersion`
2. Server responds with hardcoded version (not negotiated)
3. No validation of version compatibility
4. No `MCP-Protocol-Version` header enforcement on subsequent requests

## The Gap

**Spec Violation:**

- MCP Lifecycle spec requires proper version negotiation
- HTTP transport requires `MCP-Protocol-Version` header on all requests
- Missing client/server version compatibility checking

**Missing Implementation:**

- No version compatibility matrix
- No graceful version downgrade/upgrade handling
- No header validation on non-initialize requests

## TODOs to Close Gap

### 1. Implement Version Negotiation Logic

- [ ] Define supported protocol versions in handler config
- [ ] Implement version selection algorithm (prefer latest mutually supported)
- [ ] Add version compatibility checking in `initialize` handler
- [ ] Return appropriate version in `InitializeResult`

### 2. Add Protocol Version Header Validation

```go
const mcpProtocolVersionHeader = "MCP-Protocol-Version"

func (h *StreamingHTTPHandler) validateProtocolVersion(r *http.Request, sessionVersion string) error {
    // Validate header matches negotiated session version
}
```

### 3. Update Request Handlers

- [ ] Add version header validation to `handlePostMCP`
- [ ] Add version header validation to `handleGetMCP`
- [ ] Return 400 Bad Request for missing/invalid version headers
- [ ] Skip version validation only for initialize requests

### 4. Enhance Session Management

- [ ] Store negotiated protocol version in session metadata
- [ ] Load session version during session lookup
- [ ] Validate all requests against session's negotiated version

### 5. Add Version Compatibility Framework

```go
type VersionNegotiator interface {
    NegotiateVersion(clientVersion string) (serverVersion string, compatible bool)
    IsCompatible(clientVersion, serverVersion string) bool
}
```

### 6. Update Initialize Flow

- [ ] Extract client protocol version from initialize request
- [ ] Run version negotiation algorithm
- [ ] Store negotiated version in session
- [ ] Return negotiated version in initialize response
- [ ] Disconnect if no compatible version found

### 7. Add Error Handling

- [ ] Specific error responses for version incompatibility
- [ ] Proper error messages indicating supported versions
- [ ] Logging for version negotiation failures

**Priority:** HIGH - Required for MCP spec compliance
**Estimated Effort:** Medium (3-4 days)
**Dependencies:** Session management updates
