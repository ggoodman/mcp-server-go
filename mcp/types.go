package mcp

// Basic types
// Role indicates the role of a message author.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type LoggingLevel string

// LoggingLevel represents structured log severity.
const (
	// Logging level constants.
	LoggingLevelDebug     LoggingLevel = "debug"
	LoggingLevelInfo      LoggingLevel = "info"
	LoggingLevelNotice    LoggingLevel = "notice"
	LoggingLevelWarning   LoggingLevel = "warning"
	LoggingLevelError     LoggingLevel = "error"
	LoggingLevelCritical  LoggingLevel = "critical"
	LoggingLevelAlert     LoggingLevel = "alert"
	LoggingLevelEmergency LoggingLevel = "emergency"
)

// IsValidLoggingLevel reports whether the provided level is one of the
// protocol-defined syslog severities.
func IsValidLoggingLevel(level LoggingLevel) bool {
	switch level {
	case LoggingLevelDebug,
		LoggingLevelInfo,
		LoggingLevelNotice,
		LoggingLevelWarning,
		LoggingLevelError,
		LoggingLevelCritical,
		LoggingLevelAlert,
		LoggingLevelEmergency:
		return true
	default:
		return false
	}
}

// Capabilities
// ClientCapabilities advertises client features.
type ClientCapabilities struct {
	Roots *struct {
		ListChanged bool `json:"listChanged"`
	} `json:"roots,omitempty"`
	Sampling    *struct{} `json:"sampling,omitempty"`
	Elicitation *struct{} `json:"elicitation,omitempty"`
}

// ServerCapabilities advertises server features.
type ServerCapabilities struct {
	Logging *struct{} `json:"logging,omitempty"`
	Prompts *struct {
		ListChanged bool `json:"listChanged"`
	} `json:"prompts,omitempty"`
	Resources *struct {
		ListChanged bool `json:"listChanged"`
		Subscribe   bool `json:"subscribe"`
	} `json:"resources,omitempty"`
	Tools *struct {
		ListChanged bool `json:"listChanged"`
	} `json:"tools,omitempty"`
	Completions *struct{} `json:"completions,omitempty"`
}

// ImplementationInfo describes the implementation name and version.
type ImplementationInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Title   string `json:"title,omitzero"`
}

// Content types
// ContentBlock is a typed content part of a message.
type ContentBlock struct {
	Type string `json:"type"`
	// For TextContent
	Text string `json:"text,omitzero"`
	// For ImageContent and AudioContent
	Data     string `json:"data,omitzero"`
	MimeType string `json:"mimeType,omitzero"`
	// For EmbeddedResource
	Resource *ResourceContents `json:"resource,omitempty"`
	// For ResourceLink
	URI         string `json:"uri,omitzero"`
	Name        string `json:"name,omitzero"`
	Description string `json:"description,omitzero"`
}

// Annotations
// Annotations provide optional routing/prioritization hints.
type Annotations struct {
	Audience []Role  `json:"audience,omitempty"`
	Priority float64 `json:"priority,omitzero"`
}

// Tools
// Tool describes a callable tool and its input schema.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema ToolInputSchema `json:"inputSchema"`
	// OutputSchema optionally declares the structure of structuredContent
	// in CallToolResult for this tool.
	OutputSchema *ToolOutputSchema `json:"outputSchema,omitempty"`
}

// ToolInputSchema is a JSON-schema-like description of tool input.
type ToolInputSchema struct {
	Type                 string                    `json:"type"`
	Properties           map[string]SchemaProperty `json:"properties,omitempty"`
	Required             []string                  `json:"required,omitempty"`
	AdditionalProperties bool                      `json:"additionalProperties,omitzero"`
}

// ToolOutputSchema mirrors ToolInputSchema but omits additionalProperties.
// The schema must be an object shape.
type ToolOutputSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]SchemaProperty `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

// SchemaProperty is a simplified schema node used in tool/elicitation schemas.
type SchemaProperty struct {
	Type        string                    `json:"type,omitempty"`
	Description string                    `json:"description,omitzero"`
	Items       *SchemaProperty           `json:"items,omitempty"`
	Properties  map[string]SchemaProperty `json:"properties,omitempty"`
	Enum        []any                     `json:"enum,omitempty"`
}

// ToolAnnotations constrain the intended audience for a tool.
type ToolAnnotations struct {
	Audience []Role `json:"audience,omitempty"`
}

// Resources
// Resource represents an addressable resource.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`
	MimeType    string `json:"mimeType,omitzero"`
}

// ResourceTemplate describes a template for resource URIs.
type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`
	MimeType    string `json:"mimeType,omitzero"`
}

// ResourceContents is the value of a resource read.
type ResourceContents struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitzero"`
	// For TextResourceContents
	Text string `json:"text,omitzero"`
	// For BlobResourceContents
	Blob string `json:"blob,omitzero"`
}

// ResourceLink references another resource.
type ResourceLink struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`
	MimeType    string `json:"mimeType,omitzero"`
}

// Prompts
// Prompt describes a named prompt the server can provide.
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitzero"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument describes a single prompt argument.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`
	Required    bool   `json:"required,omitzero"`
}

// PromptMessage is a message used in a prompt.
type PromptMessage struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// Sampling
// SamplingMessage is a message used as input to model sampling.
type SamplingMessage struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ModelPreferences encode model selection tradeoffs.
type ModelPreferences struct {
	Hints                []ModelHint `json:"hints,omitempty"`
	CostPriority         float64     `json:"costPriority,omitzero"`
	SpeedPriority        float64     `json:"speedPriority,omitzero"`
	IntelligencePriority float64     `json:"intelligencePriority,omitzero"`
}

// ModelHint supplies model-specific guidance.
type ModelHint struct {
	Name string `json:"name,omitzero"`
}

// Roots
// Root identifies a workspace root.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitzero"`
}

// Completion
// ResourceReference identifies the target of completion.
type ResourceReference struct {
	Type string `json:"type"`
	URI  string `json:"uri"`
}

// CompleteArgument is the item to complete for a resource reference.
type CompleteArgument struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Completion contains completion results for a reference.
type Completion struct {
	Values  []string `json:"values"`
	Total   int      `json:"total,omitzero"`
	HasMore bool     `json:"hasMore,omitzero"`
}

// Elicitation
// ElicitationSchema is a simplified schema for elicitation prompts.
type ElicitationSchema struct {
	Type       string                               `json:"type"`
	Properties map[string]PrimitiveSchemaDefinition `json:"properties"`
	Required   []string                             `json:"required,omitempty"`
}

// PrimitiveSchemaDefinition is a leaf schema node for elicitation.
type PrimitiveSchemaDefinition struct {
	Type        string `json:"type"`
	Description string `json:"description,omitzero"`
	// For NumberSchema
	Minimum float64 `json:"minimum,omitzero"`
	Maximum float64 `json:"maximum,omitzero"`
	// For EnumSchema
	Enum []any `json:"enum,omitempty"`
}

// LatestProtocolVersion is the latest version of the protocol.
const LatestProtocolVersion = "2025-06-18"
