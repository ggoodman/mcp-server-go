package echo

import (
	"context"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpserver"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// New constructs a simple server with a single "echo" tool using TypedTool.
// It demonstrates the static tools surface in the mcpserver package.
func New() mcpserver.ServerCapabilities {
	echoDesc := mcp.Tool{
		Name:        "echo",
		Description: "Echo a message back to the caller",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]mcp.SchemaProperty{
				"message": {Type: "string"},
			},
			Required: []string{"message"},
		},
	}

	type EchoArgs struct {
		Message string `json:"message"`
	}

	tools := mcpserver.NewStaticTools(
		mcpserver.TypedTool(echoDesc, func(ctx context.Context, _ sessions.Session, a EchoArgs) (*mcp.CallToolResult, error) {
			return mcpserver.TextResult("you said: " + a.Message), nil
		}),
	)

	return mcpserver.NewServer(
		mcpserver.WithServerInfo(mcp.ImplementationInfo{Name: "examples-echo", Version: "0.1.0"}),
		mcpserver.WithToolsOptions(
			mcpserver.WithStaticToolsContainer(tools),
		),
	)
}
