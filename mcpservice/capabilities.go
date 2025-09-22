// Package mcpserver defines the capability interfaces that an MCP Server
// implementation exposes to the Streaming HTTP handler in this repository.
//
// The handler discovers capabilities at runtime on a per-session basis, and
// translates method calls on these interfaces into MCP JSON-RPC messages as
// defined by the MCP specification (see docs/mcp.md). Implementations may be
// static (same capabilities for all sessions) or dynamic (vary by session) but
// MUST be safe for concurrent use and respect the provided context for
// cancellation and deadlines.
//
// Conventions used throughout this package:
//   - Capability discovery methods return (cap, ok, err). A false ok indicates
//     that the capability is not supported for the given session; err should be
//     reserved for transient or internal failures while determining support.
//   - All methods accept a context.Context which MUST be honored for
//     cancellation. Implementations should return promptly when the context is
//     canceled or its deadline is exceeded.
//   - The sessions.Session value is the unit of isolation. Implementations
//     SHOULD treat it as the boundary for authorization, tenancy and resource
//     visibility.
//   - Pagination uses the Page[T] type in this package; a nil cursor requests
//     the first page. Implementations SHOULD populate NextCursor when more data
//     is available.
package mcpservice

import (
	"context"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

type ServerCapabilities interface {
	// GetServerInfo returns static implementation information about the server
	// that is surfaced in initialize results (name, version, etc.).
	//
	// This method MAY be called multiple times and SHOULD be inexpensive.
	// Return an error only for unexpected failures; the handler will translate
	// it to an MCP error.
	GetServerInfo(ctx context.Context, session sessions.Session) (mcp.ImplementationInfo, error)

	// GetPreferredProtocolVersion returns the server's preferred MCP protocol version
	// for this session. If ok is false, the handler should fall back to the client's
	// requested version.
	GetPreferredProtocolVersion(ctx context.Context) (version string, ok bool, err error)

	// GetInstructions returns optional human-readable instructions that should be
	// surfaced to the client during initialization. If ok is false, no instructions
	// will be included in the initialize result.
	GetInstructions(ctx context.Context, session sessions.Session) (instructions string, ok bool, err error)

	// GetResourcesCapability returns the resources capability for the session.
	// If ok is false, the server does not support resources for this session
	// and the handler will not advertise resources support.
	//
	// Implementations may return a session-scoped value. The returned value
	// MUST be safe for concurrent use.
	GetResourcesCapability(ctx context.Context, session sessions.Session) (cap ResourcesCapability, ok bool, err error)

	// GetToolsCapability returns the tools capability if supported by the server
	// for the given session. If ok is false, the handler will not advertise tool
	// support in the server capabilities.
	//
	// Implementations may return a session-scoped value. The returned value
	// MUST be safe for concurrent use.
	GetToolsCapability(ctx context.Context, session sessions.Session) (cap ToolsCapability, ok bool, err error)

	// GetPromptsCapability returns the prompts capability if supported by the server
	// for the given session. If ok is false, the handler will not advertise prompt
	// support in the server capabilities.
	//
	// Implementations may return a session-scoped value. The returned value
	// MUST be safe for concurrent use.
	GetPromptsCapability(ctx context.Context, session sessions.Session) (cap PromptsCapability, ok bool, err error)

	// GetLoggingCapability returns the logging capability if supported by the server
	// for the given session. If ok is false, the handler will not advertise logging
	// support in the server capabilities.
	//
	// Implementations may return a session-scoped value. The returned value
	// MUST be safe for concurrent use.
	GetLoggingCapability(ctx context.Context, session sessions.Session) (cap LoggingCapability, ok bool, err error)

	// GetCompletionsCapability returns the completions capability if supported by the server
	// for the given session. If ok is false, the handler will not advertise completions
	// support in the server capabilities.
	//
	// Implementations may return a session-scoped value. The returned value
	// MUST be safe for concurrent use.
	GetCompletionsCapability(ctx context.Context, session sessions.Session) (cap CompletionsCapability, ok bool, err error)
}

// ResourcesCapability defines the basic resource operations supported by the server.
// Implementations may be static (the same for every session) or dynamic (vary by
// session). All methods MUST be safe for concurrent use.
type ResourcesCapability interface {
	// ListResources returns a (possibly paginated) list of resources available to the session.
	//
	// A nil cursor requests the first page. When more results are available,
	// Page.NextCursor SHOULD be set. See Page[T] for details.
	ListResources(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Resource], error)

	// ListResourceTemplates returns a (possibly paginated) list of resource templates.
	//
	// A nil cursor requests the first page. When more results are available,
	// Page.NextCursor SHOULD be set.
	ListResourceTemplates(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.ResourceTemplate], error)

	// ReadResource returns the contents for a specific resource URI.
	//
	// Implementations SHOULD return mcp.Content values appropriate for the
	// resource (e.g., text, references). Unknown URIs SHOULD result in a
	// descriptive error that the handler can surface to the client.
	ReadResource(ctx context.Context, session sessions.Session, uri string) ([]mcp.ResourceContents, error)

	// GetSubscriptionCapability returns an optional capability for managing
	// per-session resource subscriptions. If ok is false, subscriptions are not supported.
	// The server will use the return value to decide if it advertises the "subscribe" capability.
	//
	// The returned value MUST be safe for concurrent use. Subscribe/Unsubscribe
	// MUST be idempotent with respect to duplicate calls for the same
	// (session, uri) pair.
	GetSubscriptionCapability(ctx context.Context, session sessions.Session) (cap ResourceSubscriptionCapability, ok bool, err error)

	// GetListChangedCapability returns an optional capability that, when present,
	// allows the handler to register a callback to be invoked when the resource
	// list changes (additions, removals, or metadata changes). If ok is false,
	// list-changed notifications are not supported.
	// The server will use the return value to decide if it advertises the
	// "listChanged" capability.
	GetListChangedCapability(ctx context.Context, session sessions.Session) (cap ResourceListChangedCapability, ok bool, err error)
}

// CancelSubscription cancels an active subscription. It MUST be idempotent and
// safe to call multiple times. Implementations may return an error for logging
// or diagnostics; callers should treat cancellation as best-effort.
type CancelSubscription func(ctx context.Context) error

// NotifyResourceChangeFunc is invoked to signal that the server's resource set
// has changed for the session. The uri argument SHOULD refer to the resource
// whose presence or metadata changed. When multiple changes occur or the exact
// resource is unknown, implementations MAY pass the empty string to indicate a
// general list change.
type NotifyResourceChangeFunc func(ctx context.Context, session sessions.Session, uri string)

// ResourceSubscriptionCapability enables opt-in support for resource
// subscriptions. Subscribe should be thread-safe and return quickly; heavy
// work should be done asynchronously where possible. The returned cancel func
// will be invoked by the library on explicit unsubscribe or session close.
// Cancel will be called at-least-once and MAY be called multiple times.
type ResourceSubscriptionCapability interface {
	// Subscribe begins delivering updates for the given resource URI to the
	// client associated with the session. Implementations MAY coalesce
	// duplicate subscriptions and MUST be idempotent.
	Subscribe(ctx context.Context, session sessions.Session, uri string) (err error)

	// Unsubscribe stops delivering updates for the given resource URI for the
	// client associated with the session. MUST be idempotent.
	Unsubscribe(ctx context.Context, session sessions.Session, uri string) (err error)
}

// ResourceListChangedCapability provides list-changed notifications support.
// Register should be idempotent for the same (session, fn) pair and respect ctx
// cancellation to stop delivering callbacks.
type ResourceListChangedCapability interface {
	Register(ctx context.Context, session sessions.Session, fn NotifyResourceChangeFunc) (ok bool, err error)
}

// ToolsCapability defines the server's tools surface area.
// Implementations may be static or dynamic per session. All methods MUST be
// safe for concurrent use.
type ToolsCapability interface {
	// ListTools returns a (possibly paginated) list of tools available to the session.
	// A nil cursor requests the first page. When more results are available,
	// Page.NextCursor SHOULD be set.
	ListTools(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Tool], error)

	// CallTool invokes a named tool with the provided request payload.
	// Implementations SHOULD validate inputs and return structured MCP errors
	// when appropriate. Calls MUST be isolated per session.
	CallTool(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error)

	// GetListChangedCapability returns an optional capability that, when present,
	// allows the handler to register a callback to be invoked when the tool list
	// changes. If ok is false, list-changed notifications are not supported.
	// The server will use the return value to decide if it advertises the
	// "listChanged" capability for tools.
	GetListChangedCapability(ctx context.Context, session sessions.Session) (cap ToolListChangedCapability, ok bool, err error)
}

// NotifyToolsListChangedFunc is invoked when the server's tool list changes for
// the session (additions, removals, or metadata changes). Implementations MAY
// coalesce rapid changes and deliver fewer callbacks.
type NotifyToolsListChangedFunc func(ctx context.Context, session sessions.Session)

// ToolListChangedCapability provides tools list-changed notifications support.
// Register should be idempotent for the same (session, fn) pair and respect ctx
// cancellation to stop delivering callbacks.
type ToolListChangedCapability interface {
	Register(ctx context.Context, session sessions.Session, fn NotifyToolsListChangedFunc) (ok bool, err error)
}

// PromptsCapability defines the server's prompts surface area.
// Implementations may be static or dynamic per session. All methods MUST be
// safe for concurrent use.
type PromptsCapability interface {
	// ListPrompts returns a (possibly paginated) list of prompts available to the session.
	// A nil cursor requests the first page. When more results are available,
	// Page.NextCursor SHOULD be set.
	ListPrompts(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Prompt], error)

	// GetPrompt returns the prompt definition/messages for the given name and arguments.
	GetPrompt(ctx context.Context, session sessions.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error)

	// GetListChangedCapability returns an optional capability that, when present,
	// allows the handler to register a callback to be invoked when the prompt list
	// changes. If ok is false, list-changed notifications are not supported.
	// The server will use the return value to decide if it advertises the
	// "listChanged" capability for prompts.
	GetListChangedCapability(ctx context.Context, session sessions.Session) (cap PromptListChangedCapability, ok bool, err error)
}

// NotifyPromptsListChangedFunc is invoked when the server's prompt list changes for
// the session (additions, removals, or metadata changes). Implementations MAY
// coalesce rapid changes and deliver fewer callbacks.
type NotifyPromptsListChangedFunc func(ctx context.Context, session sessions.Session)

// PromptListChangedCapability provides prompts list-changed notifications support.
// Register should be idempotent for the same (session, fn) pair and respect ctx
// cancellation to stop delivering callbacks.
type PromptListChangedCapability interface {
	Register(ctx context.Context, session sessions.Session, fn NotifyPromptsListChangedFunc) (ok bool, err error)
}

// LoggingCapability allows the client to adjust the server's logging level for
// the session or process depending on implementation. Implementations should be
// thread-safe and return quickly; expensive work should be done asynchronously.
type LoggingCapability interface {
	// SetLevel updates the server's logging level. Implementations decide scope
	// (process-wide vs session-specific) and mapping to underlying logger(s).
	SetLevel(ctx context.Context, session sessions.Session, level mcp.LoggingLevel) error
}

// CompletionsCapability enables argument autocompletion suggestions for prompts
// and resource template arguments. Implementations SHOULD validate the reference
// and argument values and return suggestions consistent with the MCP spec.
// Implementations MUST be safe for concurrent use.
type CompletionsCapability interface {
	// Complete returns completion suggestions for the provided request.
	Complete(ctx context.Context, session sessions.Session, req *mcp.CompleteRequest) (*mcp.CompleteResult, error)
}
