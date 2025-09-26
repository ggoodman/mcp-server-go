// Package sessions defines the core session abstraction shared by MCP
// transports and server capability code. A session represents the negotiated
// protocol version, authenticated principal, and optional capability surface
// for a connected client. Transports create and persist session metadata via a
// SessionHost implementation and attach capability handles returned from
// higher-level server code (mcpservice).
//
// Layers & Roles
//
//	Transport      -> orchestrates initialize handshake, manages lifetime
//	SessionHost    -> durability & coordination (ordered client stream + internal events + metadata + KV)
//	Session object -> per-session view exposed to capability code
//
// # Host Interface
//
// SessionHost abstracts persistence and ordered fan-out semantics required by
// streaming transports:
//   - PublishSession / SubscribeSession : ordered client-visible message log (at-least-once)
//   - PublishEvent / SubscribeEvents    : server-internal coordination topics
//   - Metadata CRUD + sliding TTL       : lifecycle & revocation
//   - Bounded per-session KV            : small auxiliary state
//
// Implementations
//
//	memoryhost : in-memory reference used for tests / single-process examples
//	redishost  : Redis Streams backed implementation for horizontal scale and durability
//
// # Capabilities
//
// A Session may expose optional capability interfaces (sampling, roots,
// elicitation). Transport / engine layers interrogate these when handling
// incoming JSON-RPC requests. Absence simply means the server did not elect
// to provide that surface for the session.
//
// # Elicitation Helpers
//
// WithStrictKeys and WithRawCapture configure per-call decoding semantics for
// ElicitationCapability.Elicit. They modify ElicitConfig through functional
// options; additional options can be added without breaking callers.
//
// Example (strict elicitation, shorthand pointer form):
//
//	var input struct { Name string `json:"name" jsonschema:"minLength=1"` }
//	action, err := session.GetElicitationCapability().Elicit(ctx, "Who are you?", &input, sessions.WithStrictKeys())
//	if err != nil { return err }
//	if action != sessions.ElicitActionAccept { return }
//	// use input.Name
//
// Advanced: You can still pre-build a decoder:
//
//	dec, _ := elicitation.BindStruct(&input)
//	action, err := session.GetElicitationCapability().Elicit(ctx, "Who are you?", dec)
package sessions
