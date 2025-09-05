package hooks

import (
	"context"

	"github.com/ggoodman/mcp-streaming-http-go/mcp"
)

// Session interface is defined locally to avoid circular imports
// The sessions package will provide implementations that satisfy this interface
type Session interface {
	SessionID() string
	UserID() string
	// Add other methods as needed for capability implementations
}

// Hooks defines the interface for users of the streaming HTTP MCP server to implement their own MCP server
// logic. This library doesn't implement any of the MCP server logic or state management itself, it just provides
// the framework for handling requests and responses over HTTP.
type Hooks interface {
	Initialize(ctx context.Context, session Session, req *mcp.InitializeRequest) (*mcp.InitializeResult, error)

	// GetToolsCapability returns the implementation of the ToolsCapability interface. If nil is returned, tool-related
	// functionality will not be available.
	GetToolsCapability() ToolsCapability

	// GetResourcesCapability returns the implementation of the ResourcesCapability interface. If nil is returned,
	// resource-related functionality will not be available.
	GetResourcesCapability() ResourcesCapability

	// GetPromptsCapability returns the implementation of the PromptsCapability interface. If nil is returned,
	// prompt-related functionality will not be available.
	GetPromptsCapability() PromptsCapability

	// GetLoggingCapability returns the implementation of the LoggingCapability interface. If nil is returned,
	// logging functionality will not be available.
	GetLoggingCapability() LoggingCapability

	// GetCompletionsCapability returns the implementation of the CompletionsCapability interface. If nil is returned,
	// completion functionality will not be available.
	GetCompletionsCapability() CompletionsCapability
}

// Server capability interfaces - these handle client-to-server requests

type ToolsChangedListener func(ctx context.Context) error

type ToolsCapability interface {
	ListTools(ctx context.Context, session Session, cursor *string) (tools []mcp.Tool, nextCursor *string, err error)
	CallTool(ctx context.Context, session Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error)

	// RegisterToolsChangedListener will be called with a listener function that the implementation can call to
	// indicate that tools have changed. If the implementation does not support this functionality, it can
	// return (false, nil) and the MCP server will not register the listener. If the implementation does support
	// this functionality, it should return (true, nil) and call the listener function whenever tools change.
	RegisterToolsChangedListener(ctx context.Context, session Session, listener ToolsChangedListener) (supported bool, err error)
}

type ResourcesListChangedListener func(ctx context.Context) error
type ResourceUpdatedListener func(ctx context.Context, uri string) error

type ResourcesCapability interface {
	ListResources(ctx context.Context, session Session, cursor *string) (resources []mcp.Resource, nextCursor *string, err error)
	ListResourceTemplates(ctx context.Context, session Session, cursor *string) (templates []mcp.ResourceTemplate, nextCursor *string, err error)
	ReadResource(ctx context.Context, session Session, uri string) ([]mcp.ResourceContents, error)

	SubscribeToResource(ctx context.Context, session Session, uri string) error
	UnsubscribeFromResource(ctx context.Context, session Session, uri string) error

	// RegisterResourcesListChangedListener will be called with a listener function that the implementation can call to
	// indicate that the list of resources has changed. If the implementation does not support this functionality, it can
	// return (false, nil) and the MCP server will not register the listener. If the implementation does support
	// this functionality, it should return (true, nil) and call the listener function whenever the resource list changes.
	RegisterResourcesListChangedListener(ctx context.Context, session Session, listener ResourcesListChangedListener) (supported bool, err error)

	// RegisterResourceUpdatedListener will be called with a listener function that the implementation can call to
	// indicate that a specific resource has been updated. If the implementation does not support this functionality, it can
	// return (false, nil) and the MCP server will not register the listener. If the implementation does support
	// this functionality, it should return (true, nil) and call the listener function whenever a subscribed resource is updated.
	RegisterResourceUpdatedListener(ctx context.Context, session Session, listener ResourceUpdatedListener) (supported bool, err error)
}

type PromptsListChangedListener func(ctx context.Context) error

type PromptsCapability interface {
	ListPrompts(ctx context.Context, session Session, cursor *string) (prompts []mcp.Prompt, nextCursor *string, err error)
	GetPrompt(ctx context.Context, session Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error)

	// RegisterPromptsListChangedListener will be called with a listener function that the implementation can call to
	// indicate that the list of prompts has changed. If the implementation does not support this functionality, it can
	// return (false, nil) and the MCP server will not register the listener. If the implementation does support
	// this functionality, it should return (true, nil) and call the listener function whenever the prompt list changes.
	RegisterPromptsListChangedListener(ctx context.Context, session Session, listener PromptsListChangedListener) (supported bool, err error)
}

type LoggingMessageListener func(ctx context.Context, level mcp.LoggingLevel, data any, logger *string) error

type LoggingCapability interface {
	SetLevel(ctx context.Context, session Session, level mcp.LoggingLevel) error

	// RegisterLoggingMessageListener will be called with a listener function that the implementation can call to
	// send log messages to the client. If the implementation does not support this functionality, it can
	// return (false, nil) and the MCP server will not register the listener. If the implementation does support
	// this functionality, it should return (true, nil) and call the listener function to send log messages.
	RegisterLoggingMessageListener(ctx context.Context, session Session, listener LoggingMessageListener) (supported bool, err error)
}

type CompletionsCapability interface {
	Complete(ctx context.Context, session Session, req *mcp.CompleteRequest) (*mcp.CompleteResult, error)
}
