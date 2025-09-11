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
	Name    string `json:"name"`
	Version string `json:"version"`
	Title   string `json:"title,omitzero"`
}

// Content types
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
type Annotations struct {
	Audience []Role  `json:"audience,omitempty"`
	Priority float64 `json:"priority,omitzero"`
}

// Tools
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema ToolInputSchema `json:"inputSchema"`
}

type ToolInputSchema struct {
	Type                 string                    `json:"type"`
	Properties           map[string]SchemaProperty `json:"properties,omitempty"`
	Required             []string                  `json:"required,omitempty"`
	AdditionalProperties bool                      `json:"additionalProperties,omitzero"`
}

type SchemaProperty struct {
	Type        string                    `json:"type,omitempty"`
	Description string                    `json:"description,omitzero"`
	Items       *SchemaProperty           `json:"items,omitempty"`
	Properties  map[string]SchemaProperty `json:"properties,omitempty"`
	Enum        []any                     `json:"enum,omitempty"`
}

type ToolAnnotations struct {
	Audience []Role `json:"audience,omitempty"`
}

// Resources
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`
	MimeType    string `json:"mimeType,omitzero"`
}

type ResourceTemplate struct {
	UriTemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`
	MimeType    string `json:"mimeType,omitzero"`
}

type ResourceContents struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitzero"`
	// For TextResourceContents
	Text string `json:"text,omitzero"`
	// For BlobResourceContents
	Blob string `json:"blob,omitzero"`
}

type ResourceLink struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`
	MimeType    string `json:"mimeType,omitzero"`
}

// Prompts
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitzero"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`
	Required    bool   `json:"required,omitzero"`
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
	CostPriority         float64     `json:"costPriority,omitzero"`
	SpeedPriority        float64     `json:"speedPriority,omitzero"`
	IntelligencePriority float64     `json:"intelligencePriority,omitzero"`
}

type ModelHint struct {
	Name string `json:"name,omitzero"`
}

// Roots
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitzero"`
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
	Total   int      `json:"total,omitzero"`
	HasMore bool     `json:"hasMore,omitzero"`
}

// Elicitation
type ElicitationSchema struct {
	Type       string                               `json:"type"`
	Properties map[string]PrimitiveSchemaDefinition `json:"properties"`
	Required   []string                             `json:"required,omitempty"`
}

type PrimitiveSchemaDefinition struct {
	Type        string `json:"type"`
	Description string `json:"description,omitzero"`
	// For NumberSchema
	Minimum float64 `json:"minimum,omitzero"`
	Maximum float64 `json:"maximum,omitzero"`
	// For EnumSchema
	Enum []any `json:"enum,omitempty"`
}
