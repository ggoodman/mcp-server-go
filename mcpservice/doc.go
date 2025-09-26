// Package mcpservice provides the public server capability layer for MCP. It
// defines capability interfaces (tools, resources, prompts, logging,
// completions) plus composable helpers for building either static (predefined)
// or dynamic (per-session) implementations. Transports (streaming HTTP, stdio)
// depend on this package to discover what a server supports for a negotiated
// session and translate JSON-RPC method calls into interface invocations.
//
// Relationship to Other Packages
//
//	mcp         -> raw protocol types (requests/results, enums)
//	mcpservice  -> capability interfaces & helpers
//	sessions    -> session abstraction + host persistence
//	streaminghttp / stdio -> transport binding using an Engine internally
//
// # Capability Discovery
//
// The ServerCapabilities interface is the root entry point: transports call
// getters (e.g. GetToolsCapability) per session to obtain concrete capability
// values. Each getter returns (cap, ok, err) allowing a server to omit
// capabilities or fail transiently without forcing a global shape.
//
// # Static vs Dynamic Implementations
//
// Static helpers (ToolsContainer, ResourcesContainer, PromptsContainer) manage
// an internal thread-safe set and optionally emit change notifications.
// Dynamic helpers (NewDynamicTools, NewDynamicResources, NewDynamicPrompts)
// accept functional options representing custom list/read/call logic; they’re
// ideal for multi-tenant or data-driven servers.
//
// Summary (Capabilities vs Helpers):
//
//	Capability   | Static Container          | Dynamic Builder
//	-------------|---------------------------|-----------------------------
//	Tools        | ToolsContainer            | NewDynamicTools(...options)
//	Resources    | ResourcesContainer / FS   | NewDynamicResources(...)
//	Prompts      | PromptsContainer          | NewDynamicPrompts(...)
//	Logging      | NewSlogLevelVarLogging    | (implement interface)
//	Completions  | (implement interface)     | (implement interface)
//
// # Pagination
//
// All list operations return a Page[T] with an optional NextCursor. Static
// containers use in-memory slices; dynamic implementations decide their own
// cursor encoding. A nil cursor requests the first page.
//
// # Change Notifications
//
// Containers embed a ChangeNotifier and expose it through GetListChangedCapability
// so transports can advertise listChanged support. Dynamic variants opt-in by
// providing a ChangeSubscriber via option.
//
// # Progress Reporting
//
// Long-running tool handlers can retrieve a ProgressReporter (transport bound)
// from context using ReportProgress helper functions (see progress.go) while
// streaming partial textual results through ToolResponseWriter.
//
// # Examples
//
// Static tools + dynamic resources illustrating mixed mode:
//
//	// Static echo tool
//	type EchoArgs struct { Message string `json:"message"` }
//	tools := mcpservice.NewToolsContainer(
//	    mcpservice.TypedTool[EchoArgs](
//	        mcp.Tool{
//	            Name: "echo",
//	            Description: "Echo a message back",
//	            InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]mcp.SchemaProperty{
//	                "message": {Type: "string"},
//	            }, Required: []string{"message"}},
//	        },
//	        func(ctx context.Context, s sessions.Session, a EchoArgs) (*mcp.CallToolResult, error) {
//	            return mcpservice.TextResult(a.Message), nil
//	        },
//	    ),
//	)
//
//	// Dynamic per-session resources (user-scoped)
//	dynRes := func(ctx context.Context, s sessions.Session) (mcpservice.ResourcesCapability, bool, error) {
//	    return mcpservice.NewDynamicResources(
//	        mcpservice.WithResourcesListFunc(func(ctx context.Context, _ sessions.Session, c *string) (mcpservice.Page[mcp.Resource], error) {
//	            uri := "res://user/"+s.UserID()+"/profile"
//	            return mcpservice.NewPage([]mcp.Resource{{URI: uri, Name: "profile"}}), nil
//	        }),
//	        mcpservice.WithResourcesReadFunc(func(ctx context.Context, _ sessions.Session, uri string) ([]mcp.ResourceContents, error) {
//	            if !strings.Contains(uri, s.UserID()) { return nil, fmt.Errorf("not found") }
//	            return []mcp.ResourceContents{{URI: uri, Text: "dynamic content"}}, nil
//	        }),
//	    ), true, nil
//	}
//
//	server := mcpservice.NewServer(
//	    mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "example", Version: "0.1.0"}),
//	    mcpservice.WithToolsCapability(tools),
//	    mcpservice.WithResourcesProvider(dynRes),
//	)
//
// Implementing a custom capability: write a struct satisfying the interface
// (e.g. ToolsCapability) and return it from the corresponding getter option.
//
// Error Semantics (selector table):
//
//	Context cancellation / deadline -> return ctx.Err()
//	Not found                       -> wrap / return descriptive error (transport relays message)
//	Validation issues               -> return error; transport surfaces JSON-RPC error
//	Internal unexpected             -> return error; log at call site
//
// The package avoids opinionated error types; callers can wrap errors with
// additional context. Transports treat all non-nil errors as JSON-RPC errors.
package mcpservice
