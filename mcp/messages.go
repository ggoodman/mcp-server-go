package mcp

import "encoding/json"

type Method string

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

// Base types for pagination and metadata
type PaginatedRequest struct {
	Cursor string `json:"cursor,omitzero"`
}

type PaginatedResult struct {
	NextCursor string `json:"nextCursor,omitzero"`
}

type BaseMetadata struct {
	Meta map[string]any `json:"_meta,omitempty"`
}

// Progress and Cancellation
type ProgressToken any // string | number

type CancelledNotification struct {
	RequestID string `json:"requestId"`
	Reason    string `json:"reason,omitzero"`
}

type ProgressNotificationParams struct {
	ProgressToken ProgressToken `json:"progressToken"`
	Progress      float64       `json:"progress"`
	Total         float64       `json:"total,omitzero"`
}

// Ping
type PingRequest struct{}

// Initialize flow
type InitializeRequest struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ImplementationInfo `json:"clientInfo"`
}

type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ImplementationInfo `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitzero"`
	BaseMetadata
}

type InitializedNotification struct{}

// Tools
type ListToolsRequest struct {
	PaginatedRequest
}

type ListToolsResult struct {
	Tools []Tool `json:"tools"`
	PaginatedResult
	BaseMetadata
}

type CallToolRequestReceived struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type GetPromptRequestReceived struct {
	Name      string                     `json:"name"`
	Arguments map[string]json.RawMessage `json:"arguments,omitempty"`
}

type CreateMessageResultReceived struct {
	Role       Role            `json:"role"`
	Content    json.RawMessage `json:"content"`
	Model      string          `json:"model"`
	StopReason string          `json:"stopReason,omitzero"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type ListRootsResultReceived struct {
	Roots json.RawMessage `json:"roots"`
	Meta  json.RawMessage `json:"_meta,omitempty"`
}

type ElicitResultReceived struct {
	Values json.RawMessage `json:"values"`
	Meta   json.RawMessage `json:"_meta,omitempty"`
}

type CallToolResult struct {
	Content []ContentBlock `json:"content,omitempty"`
	IsError bool           `json:"isError,omitzero"`
	BaseMetadata
}

type ToolListChangedNotification struct{}

// Resources
type ListResourcesRequest struct {
	PaginatedRequest
}

type ListResourcesResult struct {
	Resources []Resource `json:"resources"`
	PaginatedResult
	BaseMetadata
}

type ListResourceTemplatesRequest struct {
	PaginatedRequest
}

type ListResourceTemplatesResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	PaginatedResult
	BaseMetadata
}

type ReadResourceRequest struct {
	URI string `json:"uri"`
}

type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
	BaseMetadata
}

type SubscribeRequest struct {
	URI string `json:"uri"`
}

type UnsubscribeRequest struct {
	URI string `json:"uri"`
}

type ResourceListChangedNotification struct{}

type ResourceUpdatedNotification struct {
	URI string `json:"uri"`
}

// Prompts
type ListPromptsRequest struct {
	PaginatedRequest
}

type ListPromptsResult struct {
	Prompts []Prompt `json:"prompts"`
	PaginatedResult
	BaseMetadata
}

type GetPromptRequest struct {
	Name      string                     `json:"name"`
	Arguments map[string]json.RawMessage `json:"arguments,omitempty"`
}

type GetPromptResult struct {
	Description string          `json:"description,omitzero"`
	Messages    []PromptMessage `json:"messages"`
	BaseMetadata
}

type PromptListChangedNotification struct{}

// Logging
type SetLevelRequest struct {
	Level LoggingLevel `json:"level"`
}

type LoggingMessageNotification struct {
	Level  LoggingLevel `json:"level"`
	Data   any          `json:"data"`
	Logger string       `json:"logger,omitzero"`
}

// Sampling
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

type CreateMessageResult struct {
	Role       Role           `json:"role"`
	Content    []ContentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stopReason,omitzero"`
	BaseMetadata
}

// Completion
type CompleteRequest struct {
	Ref      ResourceReference `json:"ref"`
	Argument CompleteArgument  `json:"argument"`
}

type CompleteResult struct {
	Completion Completion `json:"completion"`
	BaseMetadata
}

// Roots
type ListRootsRequest struct{}

type ListRootsResult struct {
	Roots []Root `json:"roots"`
	BaseMetadata
}

type RootsListChangedNotification struct{}

// Elicitation
type ElicitRequest struct {
	Prompt string            `json:"prompt"`
	Schema ElicitationSchema `json:"schema"`
}

type ElicitResult struct {
	Values map[string]any `json:"values"`
	BaseMetadata
}

// Empty result for operations that don't return data
type EmptyResult struct {
	BaseMetadata
}

// Server-sent versions with structured data for outgoing messages
type CallToolRequestSent struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type GetPromptRequestSent struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}
