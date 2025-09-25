// Package elicitation provides building blocks for constructing schemas used in
// the MCP Elicit request/response flow.
//
// Design goals:
//  1. Keep the *ergonomic* reflection path (give us a struct pointer and we
//     derive a flat JSON Schema) while allowing more explicit / dynamic paths.
//  2. Decouple schema construction from value decoding so that callers can
//     reuse a compiled schema with multiple decode strategies (e.g. applying
//     defaults, strict business validation, instrumentation, projections).
//  3. Preserve space for future optimizations (schema fingerprint based caching
//     / registration) and richer features without locking the public API into
//     a monolith today.
//
// Layering rationale:
//
//	SchemaProvider --> describes *what* we will ask the client for.
//	ValueDecoder   --> describes *how* to take the returned raw object and
//	                   populate a destination value, enforcing validation rules.
//	SchemaDecoder  --> simple composite that satisfies both; the most common
//	                   thing users interact with. Splitting the two lower-level
//	                   concerns keeps advanced scenarios possible without
//	                   forcing complexity onto the default path.
//
// A server normally just passes a SchemaDecoder to Elicit(). The reflection
// helper (added separately) returns one. More advanced use cases can build a
// dynamic schema struct directly and implement a custom ValueDecoder.
package elicitation

// Schema is an immutable, in-memory representation of a JSON schema fragment
// usable in the elicitation flow. Only the subset of JSON Schema supported by
// the MCP elicitation protocol should be produced (currently: a flat object of
// primitive properties with optional enum constraints). Implementations SHOULD
// ensure their Marshaled form is stable (canonical key ordering) so that
// Fingerprint() remains stable.
type Schema interface {
	// MarshalJSON returns the canonical JSON bytes representing this schema.
	MarshalJSON() ([]byte, error)
	// Fingerprint returns a stable identifier (e.g. hex encoded SHA256 of the
	// canonical JSON) for potential caching or future protocol extensions.
	Fingerprint() string
}

// SchemaProvider provides (or lazily constructs) a Schema. Implementations must
// be concurrency safe; Schema() may be called from multiple goroutines.
type SchemaProvider interface {
	Schema() (Schema, error)
}

// ValueDecoder consumes the raw JSON object map that the client returned for
// an elicitation and hydrates / validates a destination value. raw MUST be a
// map created by unmarshalling the client's JSON response. dst MUST be a non
// nil pointer.
//
// Implementations SHOULD NOT mutate dst on failure (best-effort: populate into
// a shadow structure then copy on success if needed). They MUST be concurrency
// safe.
type ValueDecoder interface {
	Decode(raw map[string]any, dst any) error
}

// SchemaDecoder composes SchemaProvider + ValueDecoder. This is what most
// call-sites will work with. Reflection & dynamic builders will return an
// implementation of this interface.
//
// NOTE: We intentionally do not (yet) include an Encode capability. If/when we
// introduce edit/update style flows we can either extend this interface or add
// a sibling.
type SchemaDecoder interface {
	SchemaProvider
	ValueDecoder
}
