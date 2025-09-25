// Package mcpserver provides building blocks for implementing MCP server
// capabilities in a composable way. It exposes capability interfaces consumed by
// the Streaming HTTP handler, plus helpers for static resources, static tools,
// and change notifications.
//
// Quick start (static):
//
//	notifier := &mcpserver.ChangeNotifier{}
//	staticRes := mcpserver.NewResourcesContainer(
//	    []mcp.Resource{{URI: "res://hello.txt", Name: "hello.txt"}},
//	    nil,
//	    map[string][]mcp.ResourceContents{
//	        "res://hello.txt": {{URI: "res://hello.txt", Text: ptr("hello")}},
//	    },
//	)
//	type EchoArgs struct { Message string `json:"message"` }
//	staticTools := mcpserver.NewToolsContainer(
//	    mcpserver.TypedTool[EchoArgs](
//	        mcp.Tool{
//	            Name:        "echo",
//	            Description: "Echo a message back to the caller",
//	            InputSchema: mcp.ToolInputSchema{
//	                Type: "object",
//	                Properties: map[string]mcp.SchemaProperty{
//	                    "message": {Type: "string"},
//	                },
//	                Required: []string{"message"},
//	            },
//	        },
//	        func(ctx context.Context, s sessions.Session, a EchoArgs) (*mcp.CallToolResult, error) {
//	            return mcpserver.TextResult("you said: " + a.Message), nil
//	        },
//	    ),
//	)
//
//	srv := mcpserver.NewServer(
//	    mcpserver.WithServerInfo(mcp.ImplementationInfo{Name: "example", Version: "1.0.0"}),
//	    mcpserver.WithResourcesCapability(staticRes),
//	    mcpserver.WithToolsCapability(staticTools),
//	)
//
// Dynamic per-session capabilities:
//
//	srv := mcpserver.NewServer(
//	    mcpserver.WithResourcesProvider(func(ctx context.Context, s sessions.Session) (mcpserver.ResourcesCapability, bool, error) {
//	        if s.UserID() == "guest" { return nil, false, nil }
//	        return mcpserver.NewDynamicResources(
//	            mcpserver.WithResourcesListFunc(func(ctx context.Context, _ sessions.Session, c *string) (mcpserver.Page[mcp.Resource], error) {
//	                return mcpserver.NewPage([]mcp.Resource{{URI: "res://user/"+s.UserID()+"/profile"}}), nil
//	            }),
//	        ), true, nil
//	    }),
//	)
//
// See server.go and capability files for full API details.
package mcpservice
