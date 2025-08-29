package hooks

import (
	"context"

	"github.com/ggoodman/mcp-streaming-http-go/mcp"
)

// Client capability interfaces for server-to-client requests
// These are fully typed since the hooks package can import mcp types

type SamplingCapability interface {
	CreateMessage(ctx context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error)
}

type RootsCapability interface {
	ListRoots(ctx context.Context) (*mcp.ListRootsResult, error)
}

type ElicitationCapability interface {
	Elicit(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error)
}
