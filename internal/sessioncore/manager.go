package sessioncore

import (
	"context"
	"fmt"

	"github.com/ggoodman/mcp-server-go/sessions"
)

type Manager struct {
	Host sessions.SessionHost
}

func (m *Manager) CreateSession(ctx context.Context, userID string, protocolVersion string, caps sessions.CapabilitySet, meta sessions.MetadataClientInfo) (*SessionHandle, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *Manager) LoadSession(ctx context.Context, sessID string, userID string, unknown string) (*SessionHandle, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *Manager) DeleteSession(ctx context.Context, sessID string) error {
	return fmt.Errorf("not implemented")
}
