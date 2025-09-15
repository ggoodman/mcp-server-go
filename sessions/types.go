package sessions

import (
	"context"

	"github.com/ggoodman/mcp-server-go/mcp"
)

// Session represents a negotiated MCP session and exposes optional
// per-session capabilities. Implementations MUST be safe for concurrent use.
type Session interface {
	SessionID() string
	UserID() string
	// ProtocolVersion is the negotiated MCP protocol version baked into the session.
	ProtocolVersion() string

	GetSamplingCapability() (cap SamplingCapability, ok bool)
	GetRootsCapability() (cap RootsCapability, ok bool)
	GetElicitationCapability() (cap ElicitationCapability, ok bool)
}

// MessageHandlerFunction handles ordered messages for a session stream.
// If the handler returns an error, the subscription will terminate with that error.
type MessageHandlerFunction func(ctx context.Context, msgID string, msg []byte) error

// ClientInfo identifies the client connecting to the server.
type ClientInfo struct {
	Name    string
	Version string
}

// SamplingCapability, when present on a session, enables the sampling surface area.
type SamplingCapability interface {
	CreateMessage(ctx context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error)
}

// RootsListChangedListener is invoked when the set of workspace roots changes.
type RootsListChangedListener func(ctx context.Context) error

// RootsCapability, when present, exposes workspace roots and change notifications.
type RootsCapability interface {
	ListRoots(ctx context.Context) (*mcp.ListRootsResult, error)

	RegisterRootsListChangedListener(ctx context.Context, listener RootsListChangedListener) (supported bool, err error)
}

// ElicitationCapability, when present, exposes the elicitation API for gathering
// structured inputs from the client.
type ElicitationCapability interface {
	Elicit(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error)
}
