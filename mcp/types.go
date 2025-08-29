package mcp

// Basic types
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type LoggingLevel string

const (
	LoggingLevelDebug     LoggingLevel = "debug"
	LoggingLevelInfo      LoggingLevel = "info"
	LoggingLevelNotice    LoggingLevel = "notice"
	LoggingLevelWarning   LoggingLevel = "warning"
	LoggingLevelError     LoggingLevel = "error"
	LoggingLevelCritical  LoggingLevel = "critical"
	LoggingLevelAlert     LoggingLevel = "alert"
	LoggingLevelEmergency LoggingLevel = "emergency"
)

// Capabilities
type ClientCapabilities struct {
	Roots *struct {
		ListChanged bool `json:"listChanged"`
	} `json:"roots,omitempty"`
	Sampling    *struct{} `json:"sampling,omitempty"`
	Elicitation *struct{} `json:"elicitation,omitempty"`
}

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
}

type ImplementationInfo struct {
	Name    string  `json:"name"`
	Version string  `json:"version"`
	Title   *string `json:"title,omitempty"`
}

// Content types
type ContentBlock struct {
	Type string `json:"type"`
	// For TextContent
	Text *string `json:"text,omitempty"`
	// For ImageContent and AudioContent
	Data     *string `json:"data,omitempty"`
	MimeType *string `json:"mimeType,omitempty"`
	// For EmbeddedResource
	Resource *ResourceContents `json:"resource,omitempty"`
	// For ResourceLink
	URI         *string `json:"uri,omitempty"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// Annotations
type Annotations struct {
	Audience []Role   `json:"audience,omitempty"`
	Priority *float64 `json:"priority,omitempty"`
}

// Tools
type Tool struct {
	Name        string          `json:"name"`
	Description *string         `json:"description,omitempty"`
	InputSchema ToolInputSchema `json:"inputSchema"`
}

type ToolInputSchema struct {
	Type                 string                    `json:"type"`
	Properties           map[string]SchemaProperty `json:"properties,omitempty"`
	Required             []string                  `json:"required,omitempty"`
	AdditionalProperties *bool                     `json:"additionalProperties,omitempty"`
}

type SchemaProperty struct {
	Type        string                    `json:"type,omitempty"`
	Description *string                   `json:"description,omitempty"`
	Items       *SchemaProperty           `json:"items,omitempty"`
	Properties  map[string]SchemaProperty `json:"properties,omitempty"`
	Enum        []any                     `json:"enum,omitempty"`
}

type ToolAnnotations struct {
	Audience []Role `json:"audience,omitempty"`
}

// Resources
type Resource struct {
	URI         string  `json:"uri"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	MimeType    *string `json:"mimeType,omitempty"`
}

type ResourceTemplate struct {
	UriTemplate string  `json:"uriTemplate"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	MimeType    *string `json:"mimeType,omitempty"`
}

type ResourceContents struct {
	URI      string  `json:"uri"`
	MimeType *string `json:"mimeType,omitempty"`
	// For TextResourceContents
	Text *string `json:"text,omitempty"`
	// For BlobResourceContents
	Blob *string `json:"blob,omitempty"`
}

type ResourceLink struct {
	URI         string  `json:"uri"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	MimeType    *string `json:"mimeType,omitempty"`
}

// Prompts
type Prompt struct {
	Name        string           `json:"name"`
	Description *string          `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Required    *bool   `json:"required,omitempty"`
}

type PromptMessage struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// Sampling
type SamplingMessage struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

type ModelPreferences struct {
	Hints                []ModelHint `json:"hints,omitempty"`
	CostPriority         *float64    `json:"costPriority,omitempty"`
	SpeedPriority        *float64    `json:"speedPriority,omitempty"`
	IntelligencePriority *float64    `json:"intelligencePriority,omitempty"`
}

type ModelHint struct {
	Name *string `json:"name,omitempty"`
}

// Roots
type Root struct {
	URI  string  `json:"uri"`
	Name *string `json:"name,omitempty"`
}

// Completion
type ResourceReference struct {
	Type string `json:"type"`
	URI  string `json:"uri"`
}

type CompleteArgument struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Completion struct {
	Values  []string `json:"values"`
	Total   *int     `json:"total,omitempty"`
	HasMore *bool    `json:"hasMore,omitempty"`
}

// Elicitation
type ElicitationSchema struct {
	Type       string                               `json:"type"`
	Properties map[string]PrimitiveSchemaDefinition `json:"properties"`
	Required   []string                             `json:"required,omitempty"`
}

type PrimitiveSchemaDefinition struct {
	Type        string  `json:"type"`
	Description *string `json:"description,omitempty"`
	// For NumberSchema
	Minimum *float64 `json:"minimum,omitempty"`
	Maximum *float64 `json:"maximum,omitempty"`
	// For EnumSchema
	Enum []any `json:"enum,omitempty"`
}
