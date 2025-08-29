package mcp

import (
	"context"

	"github.com/ggoodman/mcp-streaming-http-go/sessions"
)

// Hooks defines the interface for users of the streaming HTTP MCP server to implement their own MCP server
// logic. This library doesn't implement any of the MCP server logic or state management itself, it just provides
// the framework for handling requests and responses over HTTP.
type Hooks interface {
	Initialize(ctx context.Context, session sessions.Session, req *InitializeRequest) (*InitializeResult, error)

	// GetToolsCapability returns the implementation of the ToolsCapability interface. If nil is returned, tool-related
	// functionality will not be available.
	GetToolsCapability() ToolsCapability
}

type ToolsChangedListener func(ctx context.Context) error

type ToolsCapability interface {
	ListTools(ctx context.Context, session sessions.Session) ([]Tool, error)
	CallTool(ctx context.Context, session sessions.Session, req *CallToolRequestReceived) (*CallToolResult, error)

	// RegisterToolsChangedListener will be called with a listener function that the implementation can call to
	// indicate that tools have changed. If the implementation does not support this functionality, it can
	// return (false, nil) and the MCP server will not register the listener. If the implementation does support
	// this functionality, it should return (true, nil) and call the listener function whenever tools change.
	RegisterToolsChangedListener(ctx context.Context, session sessions.Session, listener ToolsChangedListener) (bool, error)
}
