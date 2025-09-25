package engine

import (
	"context"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
)

type MessageWriter interface {
	WriteMessage(ctx context.Context, msg jsonrpc.Message) error
}

type MessageWriterFunc func(ctx context.Context, msg jsonrpc.Message) error

func NewMessageWriterFunc(f func(ctx context.Context, msg jsonrpc.Message) error) MessageWriterFunc {
	return f
}

func (f MessageWriterFunc) WriteMessage(ctx context.Context, msg jsonrpc.Message) error {
	return f(ctx, msg)
}
