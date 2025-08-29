package streaminghttp

import (
	"context"

	"github.com/ggoodman/mcp-streaming-http-go/mcp"
	"github.com/ggoodman/mcp-streaming-http-go/sessions"
)

type ModelContextProtocolHooks interface {
	Initialize(ctx context.Context, session sessions.Session, req *mcp.InitializeRequest) (*mcp.InitializeResponse, error)
}
