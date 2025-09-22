package stdio

import (
	"context"

	"github.com/ggoodman/mcp-server-go/mcpservice"
)

// Handler is a single-connection stdio transport that reads JSON-RPC messages
// from an io.Reader and writes responses to an io.Writer. By default, it uses
// os.Stdin and os.Stdout. It authenticates the peer using a UserProvider, which
// defaults to the current OS user ID.
//
// The handler is transport-only; it delegates all MCP semantics to the provided
// mcpservice.ServerCapabilities.
type Handler struct {
}

// NewHandler constructs a stdio Handler with defaults and applies options.
func NewHandler(srv mcpservice.ServerCapabilities, opts ...Option) *Handler {
}

// Serve runs the stdio event loop until EOF on the reader or the context is canceled.
// It is safe to call at most once per Handler. Serve is responsible for:
//   - JSON-RPC message framing (newline-delimited)
//   - initialize/initialized lifecycle with the provided ServerCapabilities
//   - routing requests/notifications/responses to the capabilities
//   - writing JSON-RPC responses to the writer
//
// The exact wire-format and behavior mirror the MCP stdio transport described in the
// specs; this function focuses on API shape. Implementation will be added in a
// follow-up.
func (h *Handler) Serve(ctx context.Context) error {
}
