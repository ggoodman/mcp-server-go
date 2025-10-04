package sessions

import (
	"context"
	"errors"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions/sampling"
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

	// PutData stores raw bytes value under key scoped to this session. The
	// storage is best-effort bounded by host TTL / size constraints. Callers
	// SHOULD keep values small (host-specific limits may apply). Context governs
	// cancellation. Returns a backend error on failure.
	PutData(ctx context.Context, key string, value []byte) error
	// GetData retrieves raw bytes previously stored with PutData. It returns
	// (nil, false, nil) ONLY when the key does not exist. It MUST NOT collapse
	// backend/storage errors into (ok=false). Any transport, context cancellation
	// or backend failure MUST surface via a non-nil err (caller should ignore ok
	// when err != nil). An empty stored value returns ([]byte{}, true, nil).
	GetData(ctx context.Context, key string) (value []byte, ok bool, err error)
	// DeleteData removes the key if present. Missing keys are not an error.
	DeleteData(ctx context.Context, key string) error
}

// ClientInfo identifies the client connecting to the server.
type ClientInfo struct {
	Name    string
	Version string
}

// SamplingCapability when present on a session, enables the sampling surface area.
type SamplingCapability interface {
	// CreateMessage requests the client host sample a single assistant message.
	// system: required system prompt string (may be empty for no system context).
	// user: the current user-authored message (RoleUser; exactly one content block).
	// opts: functional options configuring model preferences, history, streaming, etc.
	CreateMessage(ctx context.Context, system string, user sampling.Message, opts ...sampling.Option) (*SampleResult, error)
}

// RootsListChangedListener is invoked when the set of workspace roots changes.
type RootsListChangedListener func(ctx context.Context) error

// SampleResult is the ergonomic return type for SamplingCapability.CreateMessage.
// It intentionally mirrors the wire result shape while leaving room for future
// metadata without forcing callers onto protocol types.
type SampleResult struct {
	Message sampling.Message
	// Usage, Model, StopReason etc. can be added once surfaced by the engine.
	Model      string
	StopReason string
	Meta       map[string]any
}

// (sampling options relocated to sessions/sampling)

// (sampling builder logic moved to internal/sampling)

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
	// subject may be either:
	//   1. an elicitation.SchemaDecoder (advanced / pre-built form), OR
	//   2. a non-nil pointer to a struct (shorthand). In this case the implementation
	//      will internally call elicitation.BindStruct(subject) to derive a schema
	//      and bind the decoder to that pointer.
	//
	// Most callers should simply pass &MyStruct{} (or a pointer they keep around)
	// directly:
	//
	//  type Input struct { Name string `json:"name" jsonschema:"minLength=1"` }
	//  var in Input
	//  action, err := elicCap.Elicit(ctx, "Who are you?", &in)
	//
	// Backwards-compatible usage with an explicit SchemaDecoder still works:
	//
	//  dec, _ := elicitation.BindStruct(&in)
	//  action, err := elicCap.Elicit(ctx, "Who are you?", dec)
	//
	// If binding / schema construction fails (e.g. subject not a *struct), the
	// returned action will be ElicitActionCancel along with a descriptive error.
	Elicit(ctx context.Context, text string, subject any, opts ...ElicitOption) (ElicitAction, error)
}

// ErrInvalidElicitSubject is returned by ElicitationCapability.Elicit when the
// provided subject value is neither a SchemaDecoder nor a non-nil pointer to a struct.
// Callers can test errors.Is(err, ErrInvalidElicitSubject) to branch.
var ErrInvalidElicitSubject = errors.New("sessions: invalid elicitation subject; must be SchemaDecoder or *struct")

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
