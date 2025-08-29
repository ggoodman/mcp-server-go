package mcp

import (
	"context"

	"github.com/ggoodman/mcp-streaming-http-go/sessions"
)

type Hooks interface {
	Initialize(ctx context.Context, session sessions.Session, req *InitializeRequest) (*InitializeResponse, error)
	ListTools(ctx context.Context, session sessions.Session) ([]ToolDescriptor, error)
}
