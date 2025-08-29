package hookstest

import (
	"context"

	"github.com/ggoodman/mcp-streaming-http-go/hooks"
	"github.com/ggoodman/mcp-streaming-http-go/mcp"
)

// MockToolsCapability provides a configurable mock implementation of hooks.ToolsCapability
type MockToolsCapability struct {
	tools                        []mcp.Tool
	callToolHandler              func(ctx context.Context, session hooks.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error)
	supportsToolsChangedListener bool
	toolsChangedListener         hooks.ToolsChangedListener
}

// ToolsCapabilityOption configures MockToolsCapability
type ToolsCapabilityOption func(*MockToolsCapability)

// NewMockToolsCapability creates a new MockToolsCapability with the provided options
func NewMockToolsCapability(opts ...ToolsCapabilityOption) *MockToolsCapability {
	m := &MockToolsCapability{
		tools:                        []mcp.Tool{},
		supportsToolsChangedListener: false,
	}

	// Set default callToolHandler that checks if tool exists
	m.callToolHandler = func(ctx context.Context, session hooks.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
		// Check if the tool exists in the tools list
		for _, tool := range m.tools {
			if tool.Name == req.Name {
				// Tool found, return empty result (can be overridden by WithCallToolHandler)
				return &mcp.CallToolResult{}, nil
			}
		}
		// Tool not found, return NotFoundError
		return nil, &hooks.NotFoundError{
			Type: "tool",
			Name: req.Name,
		}
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// WithTools sets the list of tools to return from ListTools
func WithTools(tools ...mcp.Tool) ToolsCapabilityOption {
	return func(m *MockToolsCapability) {
		m.tools = tools
	}
}

// WithCallToolHandler sets a custom handler for CallTool requests
func WithCallToolHandler(handler func(ctx context.Context, session hooks.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error)) ToolsCapabilityOption {
	return func(m *MockToolsCapability) {
		m.callToolHandler = handler
	}
}

// WithToolsChangedSupport enables support for tools changed notifications
func WithToolsChangedSupport() ToolsCapabilityOption {
	return func(m *MockToolsCapability) {
		m.supportsToolsChangedListener = true
	}
}

func (m *MockToolsCapability) ListTools(ctx context.Context, session hooks.Session) ([]mcp.Tool, error) {
	return m.tools, nil
}

func (m *MockToolsCapability) CallTool(ctx context.Context, session hooks.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
	return m.callToolHandler(ctx, session, req)
}

func (m *MockToolsCapability) RegisterToolsChangedListener(ctx context.Context, session hooks.Session, listener hooks.ToolsChangedListener) (bool, error) {
	if m.supportsToolsChangedListener {
		m.toolsChangedListener = listener
		return true, nil
	}
	return false, nil
}

// TriggerToolsChanged calls the registered tools changed listener (for testing)
func (m *MockToolsCapability) TriggerToolsChanged(ctx context.Context) error {
	if m.toolsChangedListener != nil {
		return m.toolsChangedListener(ctx)
	}
	return nil
}

// MockResourcesCapability provides a configurable mock implementation of hooks.ResourcesCapability
type MockResourcesCapability struct {
	resources                            []mcp.Resource
	resourceTemplates                    []mcp.ResourceTemplate
	resourceContents                     map[string][]mcp.ResourceContents
	supportsResourcesListChangedListener bool
	supportsResourceUpdatedListener      bool
	resourcesListChangedListener         hooks.ResourcesListChangedListener
	resourceUpdatedListener              hooks.ResourceUpdatedListener
}

// ResourcesCapabilityOption configures MockResourcesCapability
type ResourcesCapabilityOption func(*MockResourcesCapability)

// NewMockResourcesCapability creates a new MockResourcesCapability with the provided options
func NewMockResourcesCapability(opts ...ResourcesCapabilityOption) *MockResourcesCapability {
	m := &MockResourcesCapability{
		resources:                            []mcp.Resource{},
		resourceTemplates:                    []mcp.ResourceTemplate{},
		resourceContents:                     make(map[string][]mcp.ResourceContents),
		supportsResourcesListChangedListener: false,
		supportsResourceUpdatedListener:      false,
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// WithResources sets the list of resources to return from ListResources
func WithResources(resources ...mcp.Resource) ResourcesCapabilityOption {
	return func(m *MockResourcesCapability) {
		m.resources = resources
	}
}

// WithResourceTemplates sets the list of resource templates to return from ListResourceTemplates
func WithResourceTemplates(templates ...mcp.ResourceTemplate) ResourcesCapabilityOption {
	return func(m *MockResourcesCapability) {
		m.resourceTemplates = templates
	}
}

// WithResourceContents sets the contents for specific resource URIs
func WithResourceContents(uri string, contents []mcp.ResourceContents) ResourcesCapabilityOption {
	return func(m *MockResourcesCapability) {
		m.resourceContents[uri] = contents
	}
}

// WithResourcesListChangedSupport enables support for resources list changed notifications
func WithResourcesListChangedSupport() ResourcesCapabilityOption {
	return func(m *MockResourcesCapability) {
		m.supportsResourcesListChangedListener = true
	}
}

// WithResourceUpdatedSupport enables support for resource updated notifications
func WithResourceUpdatedSupport() ResourcesCapabilityOption {
	return func(m *MockResourcesCapability) {
		m.supportsResourceUpdatedListener = true
	}
}

func (m *MockResourcesCapability) ListResources(ctx context.Context, session hooks.Session, cursor *string) ([]mcp.Resource, *string, error) {
	return m.resources, nil, nil
}

func (m *MockResourcesCapability) ListResourceTemplates(ctx context.Context, session hooks.Session, cursor *string) ([]mcp.ResourceTemplate, *string, error) {
	return m.resourceTemplates, nil, nil
}

func (m *MockResourcesCapability) ReadResource(ctx context.Context, session hooks.Session, uri string) ([]mcp.ResourceContents, error) {
	if contents, exists := m.resourceContents[uri]; exists {
		return contents, nil
	}
	return []mcp.ResourceContents{}, nil
}

func (m *MockResourcesCapability) SubscribeToResource(ctx context.Context, session hooks.Session, uri string) error {
	return nil
}

func (m *MockResourcesCapability) UnsubscribeFromResource(ctx context.Context, session hooks.Session, uri string) error {
	return nil
}

func (m *MockResourcesCapability) RegisterResourcesListChangedListener(ctx context.Context, session hooks.Session, listener hooks.ResourcesListChangedListener) (bool, error) {
	if m.supportsResourcesListChangedListener {
		m.resourcesListChangedListener = listener
		return true, nil
	}
	return false, nil
}

func (m *MockResourcesCapability) RegisterResourceUpdatedListener(ctx context.Context, session hooks.Session, listener hooks.ResourceUpdatedListener) (bool, error) {
	if m.supportsResourceUpdatedListener {
		m.resourceUpdatedListener = listener
		return true, nil
	}
	return false, nil
}

// TriggerResourcesListChanged calls the registered resources list changed listener (for testing)
func (m *MockResourcesCapability) TriggerResourcesListChanged(ctx context.Context) error {
	if m.resourcesListChangedListener != nil {
		return m.resourcesListChangedListener(ctx)
	}
	return nil
}

// TriggerResourceUpdated calls the registered resource updated listener (for testing)
func (m *MockResourcesCapability) TriggerResourceUpdated(ctx context.Context, uri string) error {
	if m.resourceUpdatedListener != nil {
		return m.resourceUpdatedListener(ctx, uri)
	}
	return nil
}

// MockPromptsCapability provides a configurable mock implementation of hooks.PromptsCapability
type MockPromptsCapability struct {
	prompts                            []mcp.Prompt
	promptResults                      map[string]*mcp.GetPromptResult
	supportsPromptsListChangedListener bool
	promptsListChangedListener         hooks.PromptsListChangedListener
}

// PromptsCapabilityOption configures MockPromptsCapability
type PromptsCapabilityOption func(*MockPromptsCapability)

// NewMockPromptsCapability creates a new MockPromptsCapability with the provided options
func NewMockPromptsCapability(opts ...PromptsCapabilityOption) *MockPromptsCapability {
	m := &MockPromptsCapability{
		prompts:                            []mcp.Prompt{},
		promptResults:                      make(map[string]*mcp.GetPromptResult),
		supportsPromptsListChangedListener: false,
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// WithPrompts sets the list of prompts to return from ListPrompts
func WithPrompts(prompts ...mcp.Prompt) PromptsCapabilityOption {
	return func(m *MockPromptsCapability) {
		m.prompts = prompts
	}
}

// WithPromptResult sets the result for a specific prompt name
func WithPromptResult(name string, result *mcp.GetPromptResult) PromptsCapabilityOption {
	return func(m *MockPromptsCapability) {
		m.promptResults[name] = result
	}
}

// WithPromptsListChangedSupport enables support for prompts list changed notifications
func WithPromptsListChangedSupport() PromptsCapabilityOption {
	return func(m *MockPromptsCapability) {
		m.supportsPromptsListChangedListener = true
	}
}

func (m *MockPromptsCapability) ListPrompts(ctx context.Context, session hooks.Session, cursor *string) ([]mcp.Prompt, *string, error) {
	return m.prompts, nil, nil
}

func (m *MockPromptsCapability) GetPrompt(ctx context.Context, session hooks.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error) {
	if result, exists := m.promptResults[req.Name]; exists {
		return result, nil
	}
	return &mcp.GetPromptResult{Messages: []mcp.PromptMessage{}}, nil
}

func (m *MockPromptsCapability) RegisterPromptsListChangedListener(ctx context.Context, session hooks.Session, listener hooks.PromptsListChangedListener) (bool, error) {
	if m.supportsPromptsListChangedListener {
		m.promptsListChangedListener = listener
		return true, nil
	}
	return false, nil
}

// TriggerPromptsListChanged calls the registered prompts list changed listener (for testing)
func (m *MockPromptsCapability) TriggerPromptsListChanged(ctx context.Context) error {
	if m.promptsListChangedListener != nil {
		return m.promptsListChangedListener(ctx)
	}
	return nil
}

// MockLoggingCapability provides a configurable mock implementation of hooks.LoggingCapability
type MockLoggingCapability struct {
	currentLevel                   mcp.LoggingLevel
	supportsLoggingMessageListener bool
	loggingMessageListener         hooks.LoggingMessageListener
}

// LoggingCapabilityOption configures MockLoggingCapability
type LoggingCapabilityOption func(*MockLoggingCapability)

// NewMockLoggingCapability creates a new MockLoggingCapability with the provided options
func NewMockLoggingCapability(opts ...LoggingCapabilityOption) *MockLoggingCapability {
	m := &MockLoggingCapability{
		currentLevel:                   mcp.LoggingLevelInfo,
		supportsLoggingMessageListener: false,
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// WithInitialLogLevel sets the initial log level
func WithInitialLogLevel(level mcp.LoggingLevel) LoggingCapabilityOption {
	return func(m *MockLoggingCapability) {
		m.currentLevel = level
	}
}

// WithLoggingMessageSupport enables support for logging message notifications
func WithLoggingMessageSupport() LoggingCapabilityOption {
	return func(m *MockLoggingCapability) {
		m.supportsLoggingMessageListener = true
	}
}

func (m *MockLoggingCapability) SetLevel(ctx context.Context, session hooks.Session, level mcp.LoggingLevel) error {
	m.currentLevel = level
	return nil
}

func (m *MockLoggingCapability) RegisterLoggingMessageListener(ctx context.Context, session hooks.Session, listener hooks.LoggingMessageListener) (bool, error) {
	if m.supportsLoggingMessageListener {
		m.loggingMessageListener = listener
		return true, nil
	}
	return false, nil
}

// GetCurrentLevel returns the current log level (for testing)
func (m *MockLoggingCapability) GetCurrentLevel() mcp.LoggingLevel {
	return m.currentLevel
}

// SendLogMessage calls the registered logging message listener (for testing)
func (m *MockLoggingCapability) SendLogMessage(ctx context.Context, level mcp.LoggingLevel, data any, logger *string) error {
	if m.loggingMessageListener != nil {
		return m.loggingMessageListener(ctx, level, data, logger)
	}
	return nil
}

// MockCompletionsCapability provides a configurable mock implementation of hooks.CompletionsCapability
type MockCompletionsCapability struct {
	completionHandler func(ctx context.Context, session hooks.Session, req *mcp.CompleteRequest) (*mcp.CompleteResult, error)
}

// CompletionsCapabilityOption configures MockCompletionsCapability
type CompletionsCapabilityOption func(*MockCompletionsCapability)

// NewMockCompletionsCapability creates a new MockCompletionsCapability with the provided options
func NewMockCompletionsCapability(opts ...CompletionsCapabilityOption) *MockCompletionsCapability {
	m := &MockCompletionsCapability{
		completionHandler: func(ctx context.Context, session hooks.Session, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
			return &mcp.CompleteResult{
				Completion: mcp.Completion{
					Values: []string{},
				},
			}, nil
		},
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// WithCompletionHandler sets a custom handler for completion requests
func WithCompletionHandler(handler func(ctx context.Context, session hooks.Session, req *mcp.CompleteRequest) (*mcp.CompleteResult, error)) CompletionsCapabilityOption {
	return func(m *MockCompletionsCapability) {
		m.completionHandler = handler
	}
}

func (m *MockCompletionsCapability) Complete(ctx context.Context, session hooks.Session, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	return m.completionHandler(ctx, session, req)
}

// MockHooks provides a configurable implementation of hooks.Hooks for testing
type MockHooks struct {
	initializeResult      *mcp.InitializeResult
	toolsCapability       hooks.ToolsCapability
	resourcesCapability   hooks.ResourcesCapability
	promptsCapability     hooks.PromptsCapability
	loggingCapability     hooks.LoggingCapability
	completionsCapability hooks.CompletionsCapability
}

// Option configures MockHooks
type Option func(*MockHooks)

// NewMockHooks creates a new MockHooks with sensible defaults and applies the provided options
func NewMockHooks(opts ...Option) *MockHooks {
	m := &MockHooks{
		initializeResult: &mcp.InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities: mcp.ServerCapabilities{
				Tools: &struct {
					ListChanged bool `json:"listChanged"`
				}{
					ListChanged: false,
				},
			},
			ServerInfo: mcp.ImplementationInfo{
				Name:    "test-server",
				Version: "1.0.0",
			},
		},
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// WithInitializeResult sets the initialize result
func WithInitializeResult(result *mcp.InitializeResult) Option {
	return func(m *MockHooks) {
		m.initializeResult = result
	}
}

// WithToolsCapability sets the tools capability implementation
func WithToolsCapability(capability hooks.ToolsCapability) Option {
	return func(m *MockHooks) {
		m.toolsCapability = capability
	}
}

// WithResourcesCapability sets the resources capability implementation
func WithResourcesCapability(capability hooks.ResourcesCapability) Option {
	return func(m *MockHooks) {
		m.resourcesCapability = capability
	}
}

// WithPromptsCapability sets the prompts capability implementation
func WithPromptsCapability(capability hooks.PromptsCapability) Option {
	return func(m *MockHooks) {
		m.promptsCapability = capability
	}
}

// WithLoggingCapability sets the logging capability implementation
func WithLoggingCapability(capability hooks.LoggingCapability) Option {
	return func(m *MockHooks) {
		m.loggingCapability = capability
	}
}

// WithCompletionsCapability sets the completions capability implementation
func WithCompletionsCapability(capability hooks.CompletionsCapability) Option {
	return func(m *MockHooks) {
		m.completionsCapability = capability
	}
}

// WithEmptyToolsCapability enables tools capability with an empty tool list
func WithEmptyToolsCapability() Option {
	return func(m *MockHooks) {
		m.toolsCapability = NewMockToolsCapability()
	}
}

// WithMockToolsCapability enables tools capability with the provided mock tools capability
func WithMockToolsCapability(opts ...ToolsCapabilityOption) Option {
	return func(m *MockHooks) {
		m.toolsCapability = NewMockToolsCapability(opts...)
	}
}

// WithMockResourcesCapability enables resources capability with the provided mock resources capability
func WithMockResourcesCapability(opts ...ResourcesCapabilityOption) Option {
	return func(m *MockHooks) {
		m.resourcesCapability = NewMockResourcesCapability(opts...)
	}
}

// WithMockPromptsCapability enables prompts capability with the provided mock prompts capability
func WithMockPromptsCapability(opts ...PromptsCapabilityOption) Option {
	return func(m *MockHooks) {
		m.promptsCapability = NewMockPromptsCapability(opts...)
	}
}

// WithMockLoggingCapability enables logging capability with the provided mock logging capability
func WithMockLoggingCapability(opts ...LoggingCapabilityOption) Option {
	return func(m *MockHooks) {
		m.loggingCapability = NewMockLoggingCapability(opts...)
	}
}

// WithMockCompletionsCapability enables completions capability with the provided mock completions capability
func WithMockCompletionsCapability(opts ...CompletionsCapabilityOption) Option {
	return func(m *MockHooks) {
		m.completionsCapability = NewMockCompletionsCapability(opts...)
	}
}

// WithServerName sets the server name in the initialize result
func WithServerName(name string) Option {
	return func(m *MockHooks) {
		if m.initializeResult == nil {
			m.initializeResult = &mcp.InitializeResult{}
		}
		m.initializeResult.ServerInfo.Name = name
	}
}

// WithServerVersion sets the server version in the initialize result
func WithServerVersion(version string) Option {
	return func(m *MockHooks) {
		if m.initializeResult == nil {
			m.initializeResult = &mcp.InitializeResult{}
		}
		m.initializeResult.ServerInfo.Version = version
	}
}

// Implementation of hooks.Hooks interface

func (m *MockHooks) Initialize(ctx context.Context, session hooks.Session, req *mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	return m.initializeResult, nil
}

func (m *MockHooks) GetToolsCapability() hooks.ToolsCapability {
	return m.toolsCapability
}

func (m *MockHooks) GetResourcesCapability() hooks.ResourcesCapability {
	return m.resourcesCapability
}

func (m *MockHooks) GetPromptsCapability() hooks.PromptsCapability {
	return m.promptsCapability
}

func (m *MockHooks) GetLoggingCapability() hooks.LoggingCapability {
	return m.loggingCapability
}

func (m *MockHooks) GetCompletionsCapability() hooks.CompletionsCapability {
	return m.completionsCapability
}
