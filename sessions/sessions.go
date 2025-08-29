package sessions

import (
	"context"

	"github.com/ggoodman/mcp-streaming-http-go/hooks"
	"github.com/ggoodman/mcp-streaming-http-go/internal/jsonrpc"
)

type Session interface {
	SessionID() string
	UserID() string

	ConsumeMessages(ctx context.Context, writeMsgFn MessageHandlerFunction) error

	// Client capability access - returns nil if client doesn't support the capability
	// These are now fully typed thanks to the hooks package!
	GetSamplingCapability() hooks.SamplingCapability
	GetRootsCapability() hooks.RootsCapability
	GetElicitationCapability() hooks.ElicitationCapability
}

type MessageEnvelope struct {
	MessageID string
	Message   jsonrpc.Message
}

type MessageHandlerFunction func(ctx context.Context, msg MessageEnvelope) error

type ClientInfo struct {
	Name    string
	Version string
}

type SessionMetadata interface {
	ClientInfo() ClientInfo
	GetSamplingCapability() hooks.SamplingCapability
	GetRootsCapability() hooks.RootsCapability
	GetElicitationCapability() hooks.ElicitationCapability
}

type SessionStore interface {
	CreateSession(ctx context.Context, userID string, meta SessionMetadata) (Session, error)

	LoadSession(ctx context.Context, sessID string, userID string) (Session, error)
}
