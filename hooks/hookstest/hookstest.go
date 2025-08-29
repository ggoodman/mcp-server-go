package hookstest

import (
	"context"

	"github.com/ggoodman/mcp-streaming-http-go/hooks"
	"github.com/ggoodman/mcp-streaming-http-go/mcp"
)

// MockToolsCapability provides a simple mock implementation of hooks.ToolsCapability
type MockToolsCapability struct {
	tools []mcp.Tool
}

func NewMockToolsCapability(tools ...mcp.Tool) *MockToolsCapability {
	return &MockToolsCapability{
		tools: tools,
	}
}

func (m *MockToolsCapability) ListTools(ctx context.Context, session hooks.Session) ([]mcp.Tool, error) {
	return m.tools, nil
}

func (m *MockToolsCapability) CallTool(ctx context.Context, session hooks.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{}, nil
}

func (m *MockToolsCapability) RegisterToolsChangedListener(ctx context.Context, session hooks.Session, listener hooks.ToolsChangedListener) (bool, error) {
	return false, nil
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
