// Package elicitation provides a small, purpose-built subset of JSON Schema
// for gathering structured user input via the MCP elicitation/create flow.
// It lets servers define a flat object schema (string, number, integer,
// boolean and string enums) plus basic constraints and defaults, then decode
// the client's response into an application struct or dynamic map.
//
// The design separates two concerns:
//  1. Schema construction (what to ask the user for)
//  2. Value decoding (how to validate + hydrate a destination on return)
//
// This separation enables reuse of a compiled schema with different decode
// strategies (e.g. strict vs relaxed unknown key handling) and leaves headroom
// for future enhancements (projections, metrics) without bloating the common path.
//
// Authoring Modes
//
//	Reflection  BindStruct(&T{}) derives a schema from a struct type. Exported
//	            fields become properties. Pointer fields are optional; value
//	            fields are required. A `jsonschema` struct tag supplies title,
//	            description, format, enum, enumNames, min/max constraints and
//	            optional defaults.
//	Builder     NewBuilder() with fluent property methods offers a programmatic
//	            alternative for dynamic scenarios. It mirrors the reflection
//	            feature set while remaining explicit.
//
// Supported `jsonschema` tag tokens (reflection):
//
//	title=Text
//	description=Text
//	format=email|uri|date|date-time         (string only)
//	enum=a|b|c                              (string enums)
//	enumNames=A|B|C                         (display names; length must match enum)
//	minLength=N / maxLength=N               (strings)
//	minimum=F / maximum=F                   (number / integer)
//	default=literal                         (bool|string|number; optional fields only except bool)
//
// Builder PropOptions supply the same capabilities: Required, Optional, Title,
// Description, Format, MinLength, MaxLength, Minimum, Maximum, EnumString,
// EnumNames, DefaultBool, DefaultString, DefaultNumber, Integer.
//
// Constraints & Validation
//
// Decoding enforces:
//   - Required presence for non-pointer (reflection) or Required() (builder) properties
//   - Type correctness (no coercion besides integer check on numbers)
//   - Enum membership
//   - Min/Max string length & numeric range
//   - Default application when key absent and a default exists
//
// Unknown keys are ignored by default; the higher-level elicitation
// capability can enable strict mode to reject them.
//
// Canonical Encoding & Fingerprints
//
// Both reflection and builder produce a canonical JSON encoding with stable
// key ordering, enabling deterministic SHA-256 fingerprints for caching or
// change detection. Any semantic change (including defaults) produces a new
// fingerprint string.
//
// Example (reflection):
//
//	type Input struct {
//	    Name  string `json:"name" jsonschema:"minLength=1,description=Your name"`
//	    Email *string `json:"email,omitempty" jsonschema:"format=email"`
//	}
//	var in Input
//	dec, _ := elicitation.BindStruct(&in) // SchemaDecoder
//	// pass dec to sessions.Elicit(...)
//
// Example (builder):
//
//	var raw map[string]any
//	dec := elicitation.NewBuilder().
//	    String("name", elicitation.Required(), elicitation.MinLength(1)).
//	    String("email", elicitation.Optional(), elicitation.Format("email")).
//	    MustBind(&raw)
//
//	// pass dec to sessions.Elicit(...)
//
// # Future Work
//
// The package intentionally omits nested objects, arrays and composition.
// These can be added later once the client UX contract stabilizes.
package elicitation
