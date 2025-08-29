package sessions

import (
	"context"

	"github.com/ggoodman/mcp-streaming-http-go/jsonrpc"
)

type Session interface {
	SessionID() string
	UserID() string

	ConsumeMessages(ctx context.Context, writeMsgFn MessageHandlerFunction) error
}

type MessageEnvelope struct {
	MessageID string
	Message   jsonrpc.Message
}

type MessageHandlerFunction func(ctx context.Context, msg MessageEnvelope) error

type SessionMetadata struct {
	ClientInfo struct {
		Name    string
		Version string
	}
	Capabilities struct {
		Roots *struct {
			ListChanged bool
		}
		Sampling    *struct{}
		Elicitation *struct{}
	}
}

type SessionStore interface {
	CreateSession(ctx context.Context, userID string, meta *SessionMetadata) (Session, error)

	LoadSession(ctx context.Context, sessID string, userID string) (Session, error)
}
