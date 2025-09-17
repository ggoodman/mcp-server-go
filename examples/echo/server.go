package echo

import (
	"context"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// New constructs a simple server with a single "echo" tool using TypedTool.
// It demonstrates the static tools surface in the mcpserver package.
func New() mcpservice.ServerCapabilities {
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

	tools := mcpservice.NewStaticTools(
		mcpservice.NewTool[EchoArgs](
			"echo",
			func(ctx context.Context, _ sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[EchoArgs]) error {
				_ = w.AppendText("you said: " + r.Args().Message)
				return nil
			},
			mcpservice.WithToolDescription(echoDesc.Description),
		),
	)

	return mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "examples-echo", Version: "0.1.0"}),
		mcpservice.WithToolsOptions(
			mcpservice.WithStaticToolsContainer(tools),
		),
	)
}
