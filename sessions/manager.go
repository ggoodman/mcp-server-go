package sessions

import (
	"context"
	"fmt"

	"github.com/ggoodman/mcp-streaming-http-go/broker"
	"github.com/google/uuid"
)

type SessionManager interface {
	CreateSession(ctx context.Context, userID string, opts ...SessionOption) (Session, error)

	LoadSession(ctx context.Context, sessID string, userID string) (Session, error)
}

var _ SessionManager = (*sessionManager)(nil)

type sessionManager struct {
	broker broker.Broker
}

func NewManager(broker broker.Broker) *sessionManager {
	return &sessionManager{
		broker: broker,
	}
}

func (sm *sessionManager) CreateSession(ctx context.Context, userID string, opts ...SessionOption) (Session, error) {
	session := &session{
		id:     uuid.NewString(),
		userID: userID,
		broker: sm.broker,
	}

	for _, opt := range opts {
		opt(session)
	}

	return session, nil
}

func (sm *sessionManager) LoadSession(ctx context.Context, sessID string, userID string) (Session, error) {
	// TODO: Loading sessions requires some form of persistence.
	//       We'll try to see if we can use the Storage interface for this next.
	return nil, fmt.Errorf("not implemented")
}
