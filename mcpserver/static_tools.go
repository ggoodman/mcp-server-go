package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
func NewTool[A any](name string, fn func(ctx context.Context, session sessions.Session, args A) (*mcp.CallToolResult, error), opts ...ToolOption) StaticTool {
	// Apply options
	cfg := toolConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	// Build descriptor from reflected schema
	input := reflectToMCPInputSchema[A](cfg.allowAdditionalProperties)
	desc := mcp.Tool{
		Name:        name,
		Description: cfg.description,
		InputSchema: input,
	}

	// Build handler with strict/lenient decoding as configured
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
		return fn(ctx, session, a)
	}

	return StaticTool{Descriptor: desc, Handler: handler}
}

// reflectToMCPInputSchema reflects a Go type A into a jsonschema.Schema, and
// converts it to the simplified mcp.ToolInputSchema. Unknown field policy is
// surfaced via the AdditionalProperties flag on the returned schema.
func reflectToMCPInputSchema[A any](allowAdditional bool) mcp.ToolInputSchema {
	r := &jsonschema.Reflector{
		DoNotReference:            true, // inline defs
		ExpandedStruct:            true, // put struct at root
		AllowAdditionalProperties: allowAdditional,
	}
	// Reflect from a zero value pointer to capture struct tags consistently
	s := r.Reflect(new(A))

	// Only object schemas map cleanly to MCP ToolInputSchema. If not an object,
	// expose an empty object with the configured additionalProperties policy.
	if s == nil || s.Type != "object" {
		return mcp.ToolInputSchema{
			Type:                 "object",
			Properties:           map[string]mcp.SchemaProperty{},
			AdditionalProperties: allowAdditional,
		}
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

	return mcp.ToolInputSchema{
		Type:                 "object",
		Properties:           props,
		Required:             required,
		AdditionalProperties: allowAdditional,
	}
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
	// notify listeners of change
	go st.notifier.Notify(context.Background())
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
	go st.notifier.Notify(context.Background())
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
		go st.notifier.Notify(context.Background())
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
