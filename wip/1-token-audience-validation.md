# Token Audience Validation (CRITICAL)

## Status Quo

The current authentication system validates OAuth2 access tokens but does NOT verify that tokens were specifically issued for this MCP server instance. The `auth.Authenticator` interface checks token validity but ignores the audience claim.

**Code Location:** `handler.go:325-350` - Authentication check in both POST and GET handlers

**Current Flow:**

1. Extract token from Authorization header
2. Call `h.auth.CheckAuthentication(r)`
3. Accept any valid token regardless of intended audience

## The Gap

**Security Risk:** Confused deputy vulnerability - tokens issued for other services could be used against this MCP server.

**Spec Violation:**

- RFC 8707 Resource Indicators requires audience validation
- MCP Authorization spec mandates token audience binding
- OAuth 2.1 Section 5.2 requires resource servers validate token audience

**Missing Implementation:**

- No validation that token `aud` claim contains this server's canonical URI
- No verification that token was requested with correct `resource` parameter
- No canonical URI generation for this server instance

## TODOs to Close Gap

### 1. Define Canonical Server URI

- [ ] Add method to generate canonical URI from `h.serverURL`
- [ ] Handle scheme normalization (lowercase)
- [ ] Handle port normalization (remove default ports)
- [ ] Remove fragments, normalize path trailing slashes

### 2. Extend Authenticator Interface

```go
// Add to auth package
type TokenAudienceValidator interface {
    ValidateAudience(token string, expectedAudience string) error
}
```

### 3. Update Authentication Flow

- [ ] Generate canonical URI in handler constructor
- [ ] Call audience validation after token authentication
- [ ] Return 403 Forbidden for audience mismatch (not 401)
- [ ] Add specific error messages for audience validation failures

### 4. Update Auth Implementations

- [ ] Modify OIDC authenticator to validate `aud` claim
- [ ] Add audience validation to any other auth implementations
- [ ] Ensure proper JWT parsing and claim extraction

### 5. Add Tests

- [ ] Test canonical URI generation edge cases
- [ ] Test audience validation with valid tokens
- [ ] Test audience validation with wrong audience
- [ ] Test audience validation with missing audience claim

**Priority:** CRITICAL - Must be implemented before production deployment
**Estimated Effort:** Medium (2-3 days)
**Dependencies:** None
