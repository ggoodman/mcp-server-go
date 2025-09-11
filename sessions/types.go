package sessions

import (
	"context"

	"github.com/ggoodman/mcp-server-go/mcp"
)

type Session interface {
	SessionID() string
	UserID() string
	// ProtocolVersion is the negotiated MCP protocol version baked into the session.
	ProtocolVersion() string

	GetSamplingCapability() (cap SamplingCapability, ok bool)
	GetRootsCapability() (cap RootsCapability, ok bool)
	GetElicitationCapability() (cap ElicitationCapability, ok bool)
}

type MessageHandlerFunction func(ctx context.Context, msgID string, msg []byte) error

type ClientInfo struct {
	Name    string
	Version string
}

type SamplingCapability interface {
	CreateMessage(ctx context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error)
}

type RootsListChangedListener func(ctx context.Context) error

type RootsCapability interface {
	ListRoots(ctx context.Context) (*mcp.ListRootsResult, error)

	RegisterRootsListChangedListener(ctx context.Context, listener RootsListChangedListener) (supported bool, err error)
}

type ElicitationCapability interface {
	Elicit(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error)
}
