# Resource Parameter Validation (CRITICAL)

## Status Quo

The OAuth2 authorization flow does not validate that clients included the required `resource` parameter when requesting access tokens. The MCP server accepts any valid token without verifying it was requested for this specific resource.

**Code Location:**

- `handler.go` - No resource parameter validation in auth flow
- Auth implementations - No resource parameter checking

**Current Flow:**

1. Client supposedly includes `resource=https://mcp.example.com` in auth request
2. Authorization server issues token (may or may not honor resource parameter)
3. MCP server accepts token without verifying resource binding

## The Gap

**Spec Violation:**

- MCP Authorization spec requires clients to include `resource` parameter (RFC 8707)
- Servers must validate tokens were issued for their specific resource URI
- Missing validation allows token reuse across different MCP servers

**Security Risk:**

- Tokens intended for other MCP servers could be accepted
- Breaks OAuth2 resource isolation boundaries
- Enables cross-server token replay attacks

## TODOs to Close Gap

### 1. Update Protected Resource Metadata

- [ ] Ensure PRM document clearly specifies resource URI requirements
- [ ] Document expected resource parameter format in OAuth flows
- [ ] Add guidance for client implementations

### 2. Enhance Token Validation

- [ ] Validate token was issued with correct `resource` parameter
- [ ] Check token's intended audience matches server's canonical URI
- [ ] Verify resource scope binding if using scoped tokens

### 3. Add Resource Parameter Enforcement

```go
type ResourceTokenValidator interface {
    ValidateResourceBinding(token string, expectedResource string) error
}
```

### 4. Update Authentication Interface

- [ ] Extend `auth.Authenticator` to include resource validation
- [ ] Add resource parameter validation to OIDC authenticator
- [ ] Ensure validation happens before session operations

### 5. Improve Error Responses

- [ ] Return specific error codes for resource parameter violations
- [ ] Include proper WWW-Authenticate challenges with resource info
- [ ] Log resource parameter validation failures for monitoring

### 6. Client Implementation Guidance

- [ ] Document how clients should include resource parameter
- [ ] Provide example authorization requests with resource parameter
- [ ] Add validation in any client SDK code

**Priority:** CRITICAL - Required for OAuth2 security compliance
**Estimated Effort:** Medium (2-3 days)
**Dependencies:** Token Audience Validation implementation
