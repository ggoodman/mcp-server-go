// Package streaminghttp implements the MCP streaming HTTP transport. It mounts
// as a standard net/http handler and provides ordered bidirectional JSON-RPC
// over long-lived streaming responses (Server-Sent Events style) plus normal
// request/response for RPC calls initiated by the client.
//
// Responsibilities
//   - Session creation & validation (via sessions.SessionHost)
//   - Authentication (pluggable auth.Authenticator; OIDC or manual config)
//   - Capability discovery (invokes mcpservice.ServerCapabilities getters)
//   - Ordered outbound event fan-out (progress, listChanged, resource updates)
//   - Subscription bridging (resources/updated per session + URI)
//
// Construction
//
//	h, err := streaminghttp.New(
//	    ctx,
//	    "https://api.example/mcp", // public endpoint base
//	    host,                       // sessions.SessionHost implementation
//	    server,                     // mcpservice.ServerCapabilities
//	    authenticator,              // auth.Authenticator
//	    // Security metadata inferred from authenticator (implements auth.SecurityDescriptor)
//	)
//
// Exactly one auth mode must be supplied: discovery or manual OIDC metadata.
//
// # Session Context Lifetimes
//
// The handler derives a parent context per session (decoupled from individual
// HTTP request cancellation) allowing long-lived subscription forwarders to run
// while individual RPC requests time out or disconnect. Cancellation occurs
// on explicit session termination or invalidation.
//
// # Scaling
//
// Horizontal scale relies on a shared SessionHost. Each node handles any mix
// of requests; ordering for a given session is preserved by the host's stream
// semantics, not sticky routing.
//
// Protected Resource Metadata (PRM)
//
// When OIDC discovery or manual metadata is configured, the handler exposes a
// .well-known/protected-resource-metadata endpoint advertising issuer, jwks_uri
// and supported authorization parameters, enabling clients to bootstrap without
// out-of-band configuration.
//
// # Error Handling
//
// Transport-level errors map to HTTP status codes; MCP-level errors are
// serialized as JSON-RPC error responses. Authentication failures surface a
// WWW-Authenticate challenge per the authorization spec.
//
// Example (mount in net/http):
//
//	mux := http.NewServeMux()
//	mux.Handle("/mcp/", h) // route prefix
//	http.ListenAndServe(":8080", mux)
package streaminghttp
