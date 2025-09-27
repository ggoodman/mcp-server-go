package sampling

import (
	"encoding/base64"
	"maps"

	"github.com/ggoodman/mcp-server-go/mcp"
)

// Role identifies the speaker of a sampling message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// Annotations correspond to the MCP spec annotations object and are advisory hints.
type Annotations struct {
	Audience     []Role   `json:"audience,omitempty"`
	Priority     *float64 `json:"priority,omitempty"`
	LastModified string   `json:"lastModified,omitempty"`
}

// Content represents exactly one content block in a sampling message.
type Content interface {
	isContent()
	AsContentBlock() mcp.ContentBlock
}

// Text content block.
type Text struct {
	Text        string       `json:"text"`
	Annotations *Annotations `json:"annotations,omitempty"`
}

func (Text) isContent() {}
func (t Text) AsContentBlock() mcp.ContentBlock {
	return mcp.ContentBlock{Type: mcp.ContentTypeText, Text: t.Text}
}

// Image content block (raw bytes encoded later at the transport adapter boundary).
type Image struct {
	Data        []byte       `json:"-"`
	MIMEType    string       `json:"mimeType"`
	Alt         string       `json:"alt,omitempty"`
	Annotations *Annotations `json:"annotations,omitempty"`
}

func (Image) isContent() {}
func (i Image) AsContentBlock() mcp.ContentBlock {
	enc := base64.StdEncoding.EncodeToString(i.Data)
	return mcp.ContentBlock{Type: mcp.ContentTypeImage, Data: enc, MimeType: i.MIMEType}
}

// Audio content block.
type Audio struct {
	Data        []byte       `json:"-"`
	MIMEType    string       `json:"mimeType"`
	Annotations *Annotations `json:"annotations,omitempty"`
}

func (Audio) isContent() {}
func (a Audio) AsContentBlock() mcp.ContentBlock {
	enc := base64.StdEncoding.EncodeToString(a.Data)
	return mcp.ContentBlock{Type: mcp.ContentTypeAudio, Data: enc, MimeType: a.MIMEType}
}

// Message represents a single role + single content block.
type Message struct {
	Role    Role    `json:"role"`
	Content Content `json:"content"`
}

// NewMessage constructs a Message with the given role and content.
func NewMessage(role Role, c Content) Message { return Message{Role: role, Content: c} }

// Convenience helpers.
func UserText(s string) Message      { return Message{Role: RoleUser, Content: Text{Text: s}} }
func AssistantText(s string) Message { return Message{Role: RoleAssistant, Content: Text{Text: s}} }
func SystemText(s string) Message    { return Message{Role: RoleSystem, Content: Text{Text: s}} }

// --- Options (colocated to avoid scattering sampling-specific knobs) ---

// Option is the public handle for configuring a sampling CreateMessage request.
// It deliberately does not reference internal types to avoid import cycles.
type Option interface{ apply(*options) }

// options accumulates option state before translation.
type options struct {
	history        []Message
	modelPrefs     *mcp.ModelPreferences
	temperature    *float64
	maxTokens      *int
	stopSequences  []string
	metadata       map[string]any
	includeContext string
}

type optFn func(*options)

func (f optFn) apply(o *options) { f(o) }

// WithHistory supplies prior conversation turns (excluding the current user message argument).
func WithHistory(msgs ...Message) Option {
	cp := append([]Message(nil), msgs...)
	return optFn(func(o *options) { o.history = cp })
}

// WithModelPreferences supplies model names / capabilities preferences.
func WithModelPreferences(prefs *mcp.ModelPreferences) Option {
	return optFn(func(o *options) { o.modelPrefs = prefs })
}

// WithTemperature sets the sampling temperature.
func WithTemperature(t float64) Option { return optFn(func(o *options) { o.temperature = &t }) }

// WithMaxTokens caps the number of tokens for the generated message.
func WithMaxTokens(n int) Option { return optFn(func(o *options) { o.maxTokens = &n }) }

// WithStopSequences replaces stop sequences.
func WithStopSequences(stops ...string) Option {
	cp := append([]string(nil), stops...)
	return optFn(func(o *options) { o.stopSequences = cp })
}

// WithMetadata attaches arbitrary metadata (shallow copied).
func WithMetadata(meta map[string]any) Option {
	cp := make(map[string]any, len(meta))
	maps.Copy(cp, meta)
	return optFn(func(o *options) { o.metadata = cp })
}

// WithIncludeContext requests the client include additional context (semantics defined by client impl).
func WithIncludeContext(kind string) Option {
	return optFn(func(o *options) { o.includeContext = kind })
}

// CreateSpec is the compiled form of a CreateMessage invocation prior to
// translation into the wire request. It replaces the previous exported
// Options+Accumulate pattern while keeping the functional option surface.
// This lets the engine depend only on a stable value object without exposing
// intermediate accumulation APIs.
type CreateSpec struct {
	History        []Message
	ModelPrefs     *mcp.ModelPreferences
	Temperature    *float64
	MaxTokens      *int
	StopSequences  []string
	Metadata       map[string]any
	IncludeContext string
}

// Compile applies options to the receiver and returns a populated copy.
// Typical usage: spec := sampling.CreateSpec{}.Compile(opts...)
// The receiver is ignored other than its zero-ness; a new value is built.
func (CreateSpec) Compile(opts ...Option) CreateSpec {
	var acc options
	for _, o := range opts {
		o.apply(&acc)
	}
	var spec CreateSpec
	if len(acc.history) > 0 {
		spec.History = append([]Message(nil), acc.history...)
	}
	spec.ModelPrefs = acc.modelPrefs
	spec.Temperature = acc.temperature
	spec.MaxTokens = acc.maxTokens
	if len(acc.stopSequences) > 0 {
		spec.StopSequences = append([]string(nil), acc.stopSequences...)
	}
	if acc.metadata != nil {
		cp := make(map[string]any, len(acc.metadata))
		maps.Copy(cp, acc.metadata)
		spec.Metadata = cp
	}
	spec.IncludeContext = acc.includeContext
	return spec
}
