package mcpservice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/invopop/jsonschema"
)

// ToolHandler is the function signature used to handle a tool invocation.
type ToolHandler func(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error)

// StaticTool pairs an MCP tool descriptor with its handler.
type StaticTool struct {
	Descriptor mcp.Tool
	Handler    ToolHandler
}

// ToolRequest is the container for tool call input and request metadata.
// It is generic over the typed argument struct A.
type ToolRequest[A any] struct {
	name string
	raw  json.RawMessage
	args A
}

func (r *ToolRequest[A]) Name() string                  { return r.name }
func (r *ToolRequest[A]) RawArguments() json.RawMessage { return r.raw }
func (r *ToolRequest[A]) Args() A                       { return r.args }

// ToolResponseWriterTyped extends ToolResponseWriter for typed output tools.
// It allows setting a structuredContent value of type O.
type ToolResponseWriterTyped[O any] interface {
	ToolResponseWriter
	SetStructured(v O)
}

// internal typed writer wrapper
type toolResponseWriterTyped[O any] struct {
	ToolResponseWriter
	structured any // stored as concrete O; serialized at finalize
}

func (tw *toolResponseWriterTyped[O]) SetStructured(v O) { tw.structured = v }

// NewToolWithWriter constructs a StaticTool using a typed input struct and a
// writer-based handler shape. It keeps the current typed-input decoding logic
// and enforces the writer path for composing results.
// NewTool constructs a writer-based tool with typed input A and a ToolRequest container.
func NewTool[A any](name string, fn func(ctx context.Context, session sessions.Session, w ToolResponseWriter, r *ToolRequest[A]) error, opts ...ToolOption) StaticTool {
	// Leverage existing input schema reflection and tool descriptor building
	cfg := toolConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	input := reflectToMCPInputSchema[A](cfg.allowAdditionalProperties)
	desc := mcp.Tool{
		Name:        name,
		Description: cfg.description,
		InputSchema: input,
	}

	handler := func(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
		// Strict/lenient decoding mirrors existing behavior
		var a A
		if len(req.Arguments) > 0 {
			if cfg.allowAdditionalProperties {
				if err := json.Unmarshal(req.Arguments, &a); err != nil {
					return Errorf("invalid arguments: %v", err), nil
				}
			} else {
				dec := json.NewDecoder(bytes.NewReader(req.Arguments))
				dec.DisallowUnknownFields()
				if err := dec.Decode(&a); err != nil {
					return Errorf("invalid arguments: %v", err), nil
				}
			}
		}
		w := newToolResponseWriter(ctx)
		r := &ToolRequest[A]{name: req.Name, raw: req.Arguments, args: a}
		if err := fn(ctx, session, w, r); err != nil {
			return nil, err
		}
		return w.Result(), nil
	}

	return StaticTool{Descriptor: desc, Handler: handler}
}

// TypedTool wraps a strongly typed args function into a StaticTool.
// It unmarshals req.Arguments into A and invokes fn.
func TypedTool[A any](desc mcp.Tool, fn func(ctx context.Context, session sessions.Session, args A) (*mcp.CallToolResult, error)) StaticTool {
	return StaticTool{
		Descriptor: desc,
		Handler: func(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
			var a A
			if len(req.Arguments) > 0 {
				if err := json.Unmarshal(req.Arguments, &a); err != nil {
					return Errorf("invalid arguments: %v", err), nil
				}
			}
			return fn(ctx, session, a)
		},
	}
}

// ToolOption configures NewTool behavior.
type ToolOption func(*toolConfig)

type toolConfig struct {
	description               string
	allowAdditionalProperties bool // default false (strict)
}

// WithToolDescription sets the tool description used in listings.
func WithToolDescription(desc string) ToolOption {
	return func(c *toolConfig) { c.description = desc }
}

// WithToolAllowAdditionalProperties controls whether unknown fields are allowed.
// When false (default), the generated schema sets additionalProperties=false and
// runtime decoding rejects unknown fields.
func WithToolAllowAdditionalProperties(allow bool) ToolOption {
	return func(c *toolConfig) { c.allowAdditionalProperties = allow }
}

// NewTool constructs a StaticTool from a typed args struct A. It:
// - Reflects a JSON Schema from A using invopop/jsonschema
// - Down-converts it to MCP's simplified ToolInputSchema
// - Builds the tool descriptor with the provided name and options
// - Wraps the handler with runtime JSON decoding (rejecting unknown fields by default)
// NewToolWithOutput constructs a typed-input, typed-output tool.
func NewToolWithOutput[A, O any](name string, fn func(ctx context.Context, session sessions.Session, w ToolResponseWriterTyped[O], r *ToolRequest[A]) error, opts ...ToolOption) StaticTool {
	cfg := toolConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	input := reflectToMCPInputSchema[A](cfg.allowAdditionalProperties)
	// Reflect O as output schema; always object per spec simplification.
	outSchema := reflectToMCPOutputSchema[O]()
	desc := mcp.Tool{
		Name:         name,
		Description:  cfg.description,
		InputSchema:  input,
		OutputSchema: &outSchema,
	}
	handler := func(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
		var a A
		if len(req.Arguments) > 0 {
			if cfg.allowAdditionalProperties {
				if err := json.Unmarshal(req.Arguments, &a); err != nil {
					return Errorf("invalid arguments: %v", err), nil
				}
			} else {
				dec := json.NewDecoder(bytes.NewReader(req.Arguments))
				dec.DisallowUnknownFields()
				if err := dec.Decode(&a); err != nil {
					return Errorf("invalid arguments: %v", err), nil
				}
			}
		}
		baseWriter := newToolResponseWriter(ctx)
		tw := &toolResponseWriterTyped[O]{ToolResponseWriter: baseWriter}
		r := &ToolRequest[A]{name: req.Name, raw: req.Arguments, args: a}
		if err := fn(ctx, session, tw, r); err != nil {
			return nil, err
		}
		res := baseWriter.Result()
		// Attach structuredContent if set
		if tw.structured != nil {
			// We rely on JSON marshalling of the concrete value later; store as map via json roundtrip if needed.
			// For simplicity, marshal+unmarshal once to map[string]any.
			b, err := json.Marshal(tw.structured)
			if err == nil {
				var m map[string]any
				if err2 := json.Unmarshal(b, &m); err2 == nil {
					res.StructuredContent = m
				}
			}
		}
		return res, nil
	}
	return StaticTool{Descriptor: desc, Handler: handler}
}

// reflectToMCPInputSchema reflects a Go type A into a jsonschema.Schema, and
// converts it to the simplified mcp.ToolInputSchema. Unknown field policy is
// surfaced via the AdditionalProperties flag on the returned schema.
func reflectToMCPInputSchema[A any](allowAdditional bool) mcp.ToolInputSchema {
	// Fast-path: interface{} / any or struct{} (or *struct{}) => explicitly empty schema.
	if isTriviallyEmpty[A]() {
		return mcp.ToolInputSchema{Type: "object", Properties: map[string]mcp.SchemaProperty{}, AdditionalProperties: allowAdditional}
	}
	r := &jsonschema.Reflector{
		DoNotReference:            true, // inline defs
		ExpandedStruct:            true, // put struct at root
		AllowAdditionalProperties: allowAdditional,
	}

	// Perform reflection in a panic-safe way; the upstream library currently
	// panics for some anonymous inline generic struct types. If it panics we
	// fall back to a minimal manual struct field inspection so that users can
	// still use anonymous inline argument structs without crashing.
	var s *jsonschema.Schema
	func() {
		defer func() { _ = recover() }() // swallow reflector panics
		s = r.Reflect(new(A))
	}()

	if s == nil || s.Type != "object" {
		// Try a manual reflect before giving up entirely.
		if schema, ok := manualStructSchema[A](allowAdditional); ok {
			return schema
		}
		return mcp.ToolInputSchema{Type: "object", Properties: map[string]mcp.SchemaProperty{}, AdditionalProperties: allowAdditional}
	}

	props := make(map[string]mcp.SchemaProperty)
	if s.Properties != nil {
		for el := s.Properties.Oldest(); el != nil; el = el.Next() {
			key := el.Key
			val := el.Value
			props[key] = toMCPProperty(val)
		}
	}
	var required []string
	if len(s.Required) > 0 {
		required = append(required, s.Required...)
	}

	return mcp.ToolInputSchema{Type: "object", Properties: props, Required: required, AdditionalProperties: allowAdditional}
}

// reflectToMCPOutputSchema reflects a Go type O into a mcp.ToolOutputSchema.
func reflectToMCPOutputSchema[O any]() mcp.ToolOutputSchema {
	r := &jsonschema.Reflector{DoNotReference: true, ExpandedStruct: true}
	var s *jsonschema.Schema
	func() {
		defer func() { _ = recover() }() // swallow reflector panics
		s = r.Reflect(new(O))
	}()
	if s == nil || s.Type != "object" {
		// Attempt manual reflect (best-effort) mainly for anonymous inline struct output types.
		if schema, ok := manualStructSchema[O](false); ok {
			return mcp.ToolOutputSchema{Type: schema.Type, Properties: schema.Properties, Required: schema.Required}
		}
		return mcp.ToolOutputSchema{Type: "object", Properties: map[string]mcp.SchemaProperty{}}
	}
	props := make(map[string]mcp.SchemaProperty)
	if s.Properties != nil {
		for el := s.Properties.Oldest(); el != nil; el = el.Next() {
			key := el.Key
			val := el.Value
			props[key] = toMCPProperty(val)
		}
	}
	var required []string
	if len(s.Required) > 0 {
		required = append(required, s.Required...)
	}
	return mcp.ToolOutputSchema{Type: "object", Properties: props, Required: required}
}

// manualStructSchema provides a minimal fallback schema for inline anonymous struct
// types when jsonschema reflection panics. It only handles plain struct fields with
// primitive kinds (string, bool, integer, number). Complex nested structs collapse
// to empty object properties (still usable) to keep the implementation small.
func manualStructSchema[A any](allowAdditional bool) (mcp.ToolInputSchema, bool) {
	var zero A
	t := reflect.TypeOf(zero)
	// If generic type is pointer, dereference.
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return mcp.ToolInputSchema{}, false
	}

	props := make(map[string]mcp.SchemaProperty)
	required := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		jsonTag := f.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		name := f.Name
		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			if parts[0] != "" {
				name = parts[0]
			}
		}
		// skip if name becomes "-" after tag parse
		if name == "-" {
			continue
		}
		prop := mcp.SchemaProperty{Type: goKindToJSONType(f.Type)}
		// Parse minimal jsonschema tag attributes (description only for now)
		if tag := f.Tag.Get("jsonschema"); tag != "" {
			for _, seg := range strings.Split(tag, ",") {
				kv := strings.SplitN(seg, "=", 2)
				if len(kv) == 2 && strings.TrimSpace(kv[0]) == "description" {
					prop.Description = kv[1]
				}
			}
		}
		props[name] = prop
		// Treat all fields as required by default; this matches invopop when no omitempty
		if !strings.Contains(jsonTag, "omitempty") {
			required = append(required, name)
		}
	}
	return mcp.ToolInputSchema{Type: "object", Properties: props, Required: required, AdditionalProperties: allowAdditional}, true
}

func goKindToJSONType(t reflect.Type) string {
	// Dereference pointer
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Struct:
		return "object"
	default:
		return "string" // conservative fallback
	}
}

// isTriviallyEmpty returns true when the generic type parameter A provides no
// fields for a schema: interface{} / any, struct{}, *struct{} with zero fields.
func isTriviallyEmpty[A any]() bool {
	var zero A
	t := reflect.TypeOf(zero)
	if t == nil { // untyped nil interface{}
		return true
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct && t.NumField() == 0 {
		return true
	}
	return false
}

// toMCPProperty recursively maps a jsonschema.Schema to the simplified MCP SchemaProperty.
func toMCPProperty(s *jsonschema.Schema) mcp.SchemaProperty {
	if s == nil {
		return mcp.SchemaProperty{}
	}
	p := mcp.SchemaProperty{
		Type:        s.Type,
		Description: s.Description,
	}
	if len(s.Enum) > 0 {
		p.Enum = s.Enum
	}
	// Arrays
	if s.Type == "array" && s.Items != nil {
		item := toMCPProperty(s.Items)
		p.Items = &item
	}
	// Objects
	if s.Type == "object" && s.Properties != nil {
		m := make(map[string]mcp.SchemaProperty, s.Properties.Len())
		for el := s.Properties.Oldest(); el != nil; el = el.Next() {
			key := el.Key
			val := el.Value
			m[key] = toMCPProperty(val)
		}
		p.Properties = m
	}
	return p
}

// StaticTools owns a mutable, threadsafe set of static tool descriptors and handlers.
// It is intended for simple servers that want to advertise a fixed (but
// updatable) set of tools and have the server dispatch calls automatically.
//
// StaticTools also embeds a ChangeNotifier and implements ChangeSubscriber to
// allow the tools capability to automatically expose listChanged support when
// a static container is used.
type StaticTools struct {
	mu       sync.RWMutex
	tools    []mcp.Tool             // descriptors for listing
	handlers map[string]ToolHandler // name -> handler

	notifier ChangeNotifier
}

// NewStaticTools constructs a new StaticTools container with the given tool definitions.
func NewStaticTools(defs ...StaticTool) *StaticTools {
	st := &StaticTools{}
	st.Replace(context.Background(), defs...)
	return st
}

// Snapshot returns a copy of the current tool descriptors.
func (st *StaticTools) Snapshot() []mcp.Tool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make([]mcp.Tool, len(st.tools))
	copy(out, st.tools)
	return out
}

// Replace atomically replaces the entire tool set.
func (st *StaticTools) Replace(_ context.Context, defs ...StaticTool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.tools = st.tools[:0]
	if cap(st.tools) < len(defs) {
		st.tools = make([]mcp.Tool, 0, len(defs))
	}
	st.handlers = make(map[string]ToolHandler, len(defs))
	for _, d := range defs {
		// last write wins on duplicate names
		st.tools = append(st.tools, d.Descriptor)
		if d.Handler != nil {
			st.handlers[d.Descriptor.Name] = d.Handler
		}
	}
	// notify listeners of change (best-effort; errors only indicate closed notifier)
	go func() { _ = st.notifier.Notify(context.Background()) }()
}

// Add registers a new tool if it doesn't duplicate an existing name.
// Returns true if added.
func (st *StaticTools) Add(_ context.Context, def StaticTool) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.handlers == nil {
		st.handlers = make(map[string]ToolHandler)
	}
	name := def.Descriptor.Name
	if _, exists := st.handlers[name]; exists {
		return false
	}
	// ensure no duplicate descriptor name either
	for _, t := range st.tools {
		if t.Name == name {
			return false
		}
	}
	st.tools = append(st.tools, def.Descriptor)
	if def.Handler != nil {
		st.handlers[name] = def.Handler
	}
	// notify listeners of change
	go func() { _ = st.notifier.Notify(context.Background()) }()
	return true
}

// Remove removes a tool by name. Returns true if removed.
func (st *StaticTools) Remove(_ context.Context, name string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	removed := false
	for _, t := range st.tools {
		if t.Name == name {
			removed = true
			continue
		}
		st.tools[n] = t
		n++
	}
	if removed {
		st.tools = st.tools[:n]
		delete(st.handlers, name)
		// notify listeners of change
		go func() { _ = st.notifier.Notify(context.Background()) }()
	}
	return removed
}

// Call dispatches a request to the named tool if present.
func (st *StaticTools) Call(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
	if req == nil || req.Name == "" {
		return nil, fmt.Errorf("invalid tool request: missing name")
	}
	st.mu.RLock()
	h := st.handlers[req.Name]
	st.mu.RUnlock()
	if h == nil {
		return nil, fmt.Errorf("tool not found: %s", req.Name)
	}
	return h(ctx, session, req)
}

// Subscriber implements ChangeSubscriber by returning a per-subscriber channel
// that receives a signal whenever the tool set changes.
func (st *StaticTools) Subscriber() <-chan struct{} {
	return st.notifier.Subscriber()
}

// TextResult is a small helper to build a text CallToolResult.
func TextResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.ContentBlock{{Type: "text", Text: s}}}
}

// Errorf returns an error CallToolResult with a single text block and IsError=true.
func Errorf(format string, a ...any) *mcp.CallToolResult {
	msg := fmt.Sprintf(format, a...)
	return &mcp.CallToolResult{Content: []mcp.ContentBlock{{Type: "text", Text: msg}}, IsError: true}
}
