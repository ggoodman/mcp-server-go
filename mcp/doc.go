// Package mcp contains protocol data types and constants shared across
// transports and server capability implementations. It mirrors the wire
// representation specified by the Model Context Protocol while keeping the
// surface Go-friendly (exported structs with json tags, string constants for
// method names and enumerations, helper validation functions).
//
// The package is intentionally free of transport logic: HTTP streaming,
// stdio, or any future transports import these types but implement their own
// framing, authentication and session handling. Likewise higher-level server
// packages (e.g. mcpservice) construct responses using these concrete types
// and hand them to the engine for JSON-RPC serialization.
//
// # Method Names
//
// JSON-RPC method and notification names are enumerated as Method constants
// (e.g. ToolsListMethod). Using the constants avoids typographical mistakes
// and ensures a single point of truth if the spec evolves.
//
// # Capabilities
//
// ClientCapabilities and ServerCapabilities capture negotiated feature sets.
// They are thin structs shaped to match the JSON spec. Capability advertising
// happens during initialize/initializeResult exchanges; transports simply
// marshal these types.
//
// # Pagination
//
// Many list operations use cursor-based pagination. PaginatedRequest and
// PaginatedResult are embedded in request / result envelopes to keep the core
// list types clean while offering forward-compatible metadata via BaseMetadata.
//
// # Metadata
//
// BaseMetadata allows response producers to attach implementation-defined
// metadata under the _meta key without inflating every struct with an unused
// field. Composition (embedding) keeps serialization cost minimal when unset.
//
// Example (tool result construction):
//
//	res := &mcp.CallToolResult{
//	    Content: []mcp.ContentBlock{{Type: mcp.ContentTypeText, Text: "hello"}},
//	}
//
// Example (progress notification object):
//
//	prog := mcp.ProgressNotificationParams{ProgressToken: "op1", Progress: 42, Total: 100}
//	// engine layer serializes & dispatches
//
// # Logging Levels
//
// LoggingLevel values mirror syslog severities defined by the spec. Use
// IsValidLoggingLevel to validate user-provided values in capability code.
//
// # Compatibility
//
// The LatestProtocolVersion constant reflects the most recent protocol date
// the library targets. Transports negotiate versions at runtime; application
// code can compare a session's negotiated version with this constant to gate
// optional behaviors.
package mcp
