package sessions

import (
	"context"

	"github.com/ggoodman/mcp-server-go/elicitation"
	"github.com/ggoodman/mcp-server-go/mcp"
)

// Session represents a negotiated MCP session and exposes optional
// per-session capabilities. Implementations MUST be safe for concurrent use.
type Session interface {
	SessionID() string
	UserID() string
	// ProtocolVersion is the negotiated MCP protocol version baked into the session.
	ProtocolVersion() string

	GetSamplingCapability() (cap SamplingCapability, ok bool)
	GetRootsCapability() (cap RootsCapability, ok bool)
	GetElicitationCapability() (cap ElicitationCapability, ok bool)
}

// MessageHandlerFunction handles ordered messages for a session stream.
// If the handler returns an error, the subscription will terminate with that error.
type MessageHandlerFunction func(ctx context.Context, msgID string, msg []byte) error

// ClientInfo identifies the client connecting to the server.
type ClientInfo struct {
	Name    string
	Version string
}

// SamplingCapability when present on a session, enables the sampling surface area.
type SamplingCapability interface {
	CreateMessage(ctx context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error)
}

// RootsListChangedListener is invoked when the set of workspace roots changes.
type RootsListChangedListener func(ctx context.Context) error

// RootsCapability when present, exposes workspace roots and change notifications.
type RootsCapability interface {
	ListRoots(ctx context.Context) (*mcp.ListRootsResult, error)

	RegisterRootsListChangedListener(ctx context.Context, listener RootsListChangedListener) (supported bool, err error)
}

// ElicitAction indicates the client's chosen action for an elicitation.
// Exactly one action is returned. Server logic should usually proceed only
// when Action == ElicitActionAccept and treat Decline / Cancel as a signal to
// abort or provide alternative behavior.
type ElicitAction string

const (
	ElicitActionAccept  ElicitAction = "accept"
	ElicitActionDecline ElicitAction = "decline"
	ElicitActionCancel  ElicitAction = "cancel"
)

// ElicitationCapability exposes a reflective elicitation API.
//
// Call pattern:
//
//	type Input struct { Name string `json:"name" jsonschema:"minLength=1,description=Your name"` }
//	var in Input
//	action, err := elicCap.Elicit(ctx, "Who are you?", &in)
//	if err != nil { /* transport / validation error */ }
//	if action != sessions.ElicitActionAccept { /* user declined/cancelled */ }
//	// use in.Name
//
// Behavior:
//  1. target MUST be a non-nil pointer to a struct; exported fields become schema properties.
//  2. JSON + `jsonschema` struct tags (via invopop/jsonschema) are reflected into a flat object schema.
//     Nested objects, arrays, refs and composition keywords are currently rejected to keep
//     client implementation cost low.
//  3. Pointer fields are treated as optional. Non-pointer fields present in the schema's
//     required set are enforced.
//  4. On success the target struct is populated in-place and the user action is returned.
//  5. If decoding fails (type mismatch, enum violation, missing required), an error is returned.
//
// Concurrency: Implementations MUST be safe for concurrent use. Each call derives its schema
// from type metadata and does not mutate the target until after a successful round-trip.
type ElicitationCapability interface {
	// Elicit sends an elicitation request with the provided user-facing message text.
	// The provided SchemaDecoder encapsulates both the schema (what is being asked)
	// and the decoding logic (how to hydrate the destination). The destination is
	// typically already bound inside the SchemaDecoder (e.g. via elicitation.BindStruct(&myStruct)),
	// but implementations MAY allow Decode() to accept an alternate destination when
	// invoked. The options control per-call validation semantics (like strict key
	// enforcement) and raw capture.
	Elicit(ctx context.Context, text string, decoder elicitation.SchemaDecoder, opts ...ElicitOption) (ElicitAction, error)
}

// ElicitOption configures an elicitation invocation (functional options pattern).
type ElicitOption func(*ElicitConfig)

// ElicitConfig accumulates option settings for an elicitation invocation.
// Fields are exported only so option helpers in other packages (if any) can
// apply them; prefer the provided With* helpers for forward compatibility.
type ElicitConfig struct {
	Strict bool
	RawDst *map[string]any
}

// WithStrictKeys enforces that the client returns no properties beyond those
// defined in the derived schema. Without this option, unknown keys are
// ignored to allow clients to evolve UI metadata without breaking servers.
func WithStrictKeys() ElicitOption { return func(o *ElicitConfig) { o.Strict = true } }

// WithRawCapture copies the raw returned content map into dst (if non-nil).
// The map is shallow-copied so callers can safely mutate it.
func WithRawCapture(dst *map[string]any) ElicitOption {
	return func(o *ElicitConfig) { o.RawDst = dst }
}
