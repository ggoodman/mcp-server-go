// Package elicitation provides primitives for defining and decoding flat elicitation
// schemas (restricted JSON Schema subset) for MCP 'elicitation/create' requests.
//
// Two authoring modes:
//  1. Reflection: BindStruct(&T{}) derives schema from a struct.
//  2. Builder: NewBuilder() + fluent property methods.
//
// Supported primitive types: string, number, integer, boolean, and string enums.
// Nested objects / arrays intentionally omitted (simplifies initial client surface).
//
// Reflection jsonschema tag keys (all optional unless noted):
//
//	title=Text                -> property title
//	description=Text          -> description
//	format=email|uri|date|date-time (string only)
//	enum=a|b|c                -> string enum values
//	enumNames=A|B|C           -> display names (length must match enum)
//	minLength=N               -> string minimum length
//	maxLength=N               -> string maximum length
//	minimum=F                 -> numeric/integer minimum
//	maximum=F                 -> numeric/integer maximum
//	default=true|false        -> boolean default (optional pointer field only applied if omitted)
//
// Unknown tokens ignored for forward compatibility.
//
// Builder PropOptions mirror the above:
//
//	Required(), Optional()
//	Title(string), Description(string)
//	Format(string) // validated against allowed set for strings
//	MinLength(int), MaxLength(int)
//	Minimum(float64), Maximum(float64)
//	EnumString(name, values, EnumNames(...), ...options)
//	EnumNames(displayValues...)
//	DefaultBool(bool) // only for boolean
//	Integer(name, opts...) for integer type
//
// Decoding:
//   - Unknown keys ignored (strictness enforced at higher layer).
//   - Type coercion minimal; JSON numbers (float64) cast to integer/float targets.
//   - Enum validation performed before assignment.
//   - Boolean defaults applied when property marked optional (pointer in reflection or absent in map) and not present in input.
//
// Canonical Encoding & Fingerprints:
//
//	Schemas are marshaled with a custom canonical encoder ensuring deterministic ordering:
//	  Root keys: type, properties, required (if any)
//	  Property order: struct field order (reflection) or insertion order (builder)
//	  Property key order: type, title, description, format, enum, enumNames, default, minLength, maxLength, minimum, maximum
//	The fingerprint is SHA-256 over the canonical bytes.
//
// Future extensions (not yet implemented): defaults for other types, nested objects, arrays, custom validators.
//
// # Update documentation to reflect new default semantics
//
// Defaults:
//   - Supported for boolean, string, number, integer.
//   - Reflection: `default=literal` in jsonschema tag. For numbers/integers the literal is parsed with %f.
//   - Builder: DefaultBool, DefaultString, DefaultNumber PropOptions.
//   - Exactly one default per property (panic on multiples).
//   - Defaults only allowed on optional (pointer in reflection / Optional() in builder) properties.
//   - Validation at schema build time: enum membership, min/max length, min/max numeric, integer integral check.
//   - Defaults applied only when the property key is absent in decode input.
//   - Required+default panics (early feedback, avoids silent shadowing of provided values).
//   - Reflection tag defaults cannot contain commas (tag token separator). Use builder if needed.
//
// Fingerprint impact: adding or changing a default changes canonical schema bytes -> new fingerprint.
package elicitation
