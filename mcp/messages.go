package mcp

import "encoding/json"

// Method is an MCP method identifier used in JSON-RPC messages.
type Method string

// MCP method names and notifications.
const (
	// Initialization
	InitializeMethod              Method = "initialize"
	InitializedNotificationMethod Method = "notifications/initialized"

	// Tools
	ToolsListMethod                    Method = "tools/list"
	ToolsCallMethod                    Method = "tools/call"
	ToolsListChangedNotificationMethod Method = "notifications/tools/list_changed"

	// Resources
	ResourcesListMethod                    Method = "resources/list"
	ResourcesReadMethod                    Method = "resources/read"
	ResourcesTemplatesListMethod           Method = "resources/templates/list"
	ResourcesSubscribeMethod               Method = "resources/subscribe"
	ResourcesUnsubscribeMethod             Method = "resources/unsubscribe"
	ResourcesListChangedNotificationMethod Method = "notifications/resources/list_changed"
	ResourcesUpdatedNotificationMethod     Method = "notifications/resources/updated"

	// Prompts
	PromptsListMethod                    Method = "prompts/list"
	PromptsGetMethod                     Method = "prompts/get"
	PromptsListChangedNotificationMethod Method = "notifications/prompts/list_changed"

	// Logging
	LoggingSetLevelMethod            Method = "logging/setLevel"
	LoggingMessageNotificationMethod Method = "notifications/message"

	// Sampling
	SamplingCreateMessageMethod Method = "sampling/createMessage"

	// Completion
	CompletionCompleteMethod Method = "completion/complete"

	// Roots
	RootsListMethod                    Method = "roots/list"
	RootsListChangedNotificationMethod Method = "notifications/roots/list_changed"

	// Elicitation
	ElicitationCreateMethod Method = "elicitation/create"

	// General
	PingMethod                  Method = "ping"
	CancelledNotificationMethod Method = "notifications/cancelled"
	ProgressNotificationMethod  Method = "notifications/progress"
)

// PaginatedRequest carries a cursor for paginated list requests.
type PaginatedRequest struct {
	Cursor string `json:"cursor,omitzero"`
}

// PaginatedResult carries a cursor for continuing pagination.
type PaginatedResult struct {
	NextCursor string `json:"nextCursor,omitzero"`
}

// BaseMetadata carries optional metadata for responses.
type BaseMetadata struct {
	Meta map[string]any `json:"_meta,omitempty"`
}

// ProgressToken is an identifier used to correlate progress updates.
// It may be a string or number.
type ProgressToken any // string | number

// CancelledNotification informs the peer that a request was canceled.
type CancelledNotification struct {
	RequestID string `json:"requestId"`
	Reason    string `json:"reason,omitzero"`
}

// ProgressNotificationParams conveys progress of a long-running operation.
type ProgressNotificationParams struct {
	ProgressToken ProgressToken `json:"progressToken"`
	Progress      float64       `json:"progress"`
	Total         float64       `json:"total,omitzero"`
}

// PingRequest is a no-op request used to test connectivity.
type PingRequest struct{}

// InitializeRequest starts the MCP initialization handshake.
type InitializeRequest struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ImplementationInfo `json:"clientInfo"`
}

// InitializeResult returns negotiated capabilities and server info.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ImplementationInfo `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitzero"`
	BaseMetadata
}

// InitializedNotification signals that initialization completed.
type InitializedNotification struct{}

// Tools
// ListToolsRequest requests the set of available tools.
type ListToolsRequest struct {
	PaginatedRequest
}

// ListToolsResult returns the available tools.
type ListToolsResult struct {
	Tools []Tool `json:"tools"`
	PaginatedResult
	BaseMetadata
}

// CallToolRequestReceived is the server-received representation for a tool call.
type CallToolRequestReceived struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// GetPromptRequestReceived is the server-received representation for prompt retrieval.
type GetPromptRequestReceived struct {
	Name      string                     `json:"name"`
	Arguments map[string]json.RawMessage `json:"arguments,omitempty"`
}

// CreateMessageResultReceived is the server-received representation for a sampling result.
type CreateMessageResultReceived struct {
	Role       Role            `json:"role"`
	Content    json.RawMessage `json:"content"`
	Model      string          `json:"model"`
	StopReason string          `json:"stopReason,omitzero"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

// ListRootsResultReceived contains root listing results as raw JSON.
type ListRootsResultReceived struct {
	Roots json.RawMessage `json:"roots"`
	Meta  json.RawMessage `json:"_meta,omitempty"`
}

// ElicitResultReceived contains structured values for elicitation responses.
type ElicitResultReceived struct {
	Values json.RawMessage `json:"values"`
	Meta   json.RawMessage `json:"_meta,omitempty"`
}

// CallToolResult represents a tool invocation result.
type CallToolResult struct {
	Content []ContentBlock `json:"content,omitempty"`
	IsError bool           `json:"isError,omitzero"`
	// StructuredContent contains a typed object that conforms to the tool's
	// OutputSchema when provided.
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
	BaseMetadata
}

// ToolListChangedNotification indicates the set of tools changed.
type ToolListChangedNotification struct{}

// Resources
// ListResourcesRequest requests a paginated list of resources.
type ListResourcesRequest struct {
	PaginatedRequest
}

// ListResourcesResult returns a page of resources.
type ListResourcesResult struct {
	Resources []Resource `json:"resources"`
	PaginatedResult
	BaseMetadata
}

// ListResourceTemplatesRequest requests resource templates.
type ListResourceTemplatesRequest struct {
	PaginatedRequest
}

// ListResourceTemplatesResult returns resource templates.
type ListResourceTemplatesResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	PaginatedResult
	BaseMetadata
}

// ReadResourceRequest requests the contents of a resource by URI.
type ReadResourceRequest struct {
	URI string `json:"uri"`
}

// ReadResourceResult returns resource contents.
type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
	BaseMetadata
}

// SubscribeRequest subscribes to updates for the given URI.
type SubscribeRequest struct {
	URI string `json:"uri"`
}

// UnsubscribeRequest ends a subscription for the given URI.
type UnsubscribeRequest struct {
	URI string `json:"uri"`
}

// ResourceListChangedNotification indicates the set of resources changed.
type ResourceListChangedNotification struct{}

// ResourceUpdatedNotification indicates a resource's content changed.
type ResourceUpdatedNotification struct {
	URI string `json:"uri"`
}

// Prompts
// ListPromptsRequest requests available prompts.
type ListPromptsRequest struct {
	PaginatedRequest
}

// ListPromptsResult returns available prompts.
type ListPromptsResult struct {
	Prompts []Prompt `json:"prompts"`
	PaginatedResult
	BaseMetadata
}

// GetPromptRequest requests a prompt definition by name.
type GetPromptRequest struct {
	Name      string                     `json:"name"`
	Arguments map[string]json.RawMessage `json:"arguments,omitempty"`
}

// GetPromptResult returns a prompt definition and messages.
type GetPromptResult struct {
	Description string          `json:"description,omitzero"`
	Messages    []PromptMessage `json:"messages"`
	BaseMetadata
}

// PromptListChangedNotification indicates the set of prompts changed.
type PromptListChangedNotification struct{}

// Logging
// SetLevelRequest sets the server logging level.
type SetLevelRequest struct {
	Level LoggingLevel `json:"level"`
}

// LoggingMessageNotification conveys a structured log message.
type LoggingMessageNotification struct {
	Level  LoggingLevel `json:"level"`
	Data   any          `json:"data"`
	Logger string       `json:"logger,omitzero"`
}

// Sampling
// CreateMessageRequest requests a model-generated message.
type CreateMessageRequest struct {
	Messages         []SamplingMessage `json:"messages"`
	ModelPreferences *ModelPreferences `json:"modelPreferences,omitempty"`
	SystemPrompt     string            `json:"systemPrompt,omitzero"`
	IncludeContext   string            `json:"includeContext,omitzero"`
	Temperature      float64           `json:"temperature,omitzero"`
	MaxTokens        int               `json:"maxTokens,omitzero"`
	StopSequences    []string          `json:"stopSequences,omitempty"`
	Metadata         map[string]any    `json:"metadata,omitempty"`
}

// CreateMessageResult returns a generated message.
type CreateMessageResult struct {
	Role       Role         `json:"role"`
	Content    ContentBlock `json:"content"`
	Model      string       `json:"model"`
	StopReason string       `json:"stopReason,omitzero"`
	BaseMetadata
}

// Completion
// CompleteRequest requests completion suggestions for a reference.
type CompleteRequest struct {
	Ref      ResourceReference `json:"ref"`
	Argument CompleteArgument  `json:"argument"`
}

// CompleteResult contains completion suggestions.
type CompleteResult struct {
	Completion Completion `json:"completion"`
	BaseMetadata
}

// Roots
// ListRootsRequest requests the root entries.
type ListRootsRequest struct{}

// ListRootsResult returns root entries.
type ListRootsResult struct {
	Roots []Root `json:"roots"`
	BaseMetadata
}

// RootsListChangedNotification indicates roots changed.
type RootsListChangedNotification struct{}

// Elicitation
// ElicitRequest asks for structured input per schema.
type ElicitRequest struct {
	Message         string            `json:"message"`
	RequestedSchema ElicitationSchema `json:"requestedSchema"`
}

// ElicitResult returns schema-conformant values.
type ElicitResult struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content"`
	BaseMetadata
}

// TypedElicitResult is returned by the higher-level typed elicitation helper.
// Value points to the decoded struct instance supplied by the caller. Raw
// mirrors ElicitResult.Content after normalization.
type TypedElicitResult[T any] struct {
	Action   string
	Value    *T
	Raw      map[string]any
	Metadata map[string]any
}

// Empty result for operations that don't return data
// EmptyResult is returned for operations that do not return data.
type EmptyResult struct {
	BaseMetadata
}

// Server-sent versions with structured data for outgoing messages
// CallToolRequestSent is the server-sent version of a tool call.
type CallToolRequestSent struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// GetPromptRequestSent is the server-sent version of a prompt get.
type GetPromptRequestSent struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}
