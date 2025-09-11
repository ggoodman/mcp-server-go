package sessions

import (
	"context"
	"errors"

	"github.com/ggoodman/mcp-streaming-http-go/mcp"
)

// SessionOption is a functional option for configuring a session created
// by the SessionManager. These options can be used to enable or disable
// specific capabilities for the session.
type SessionOption func(*session)

func WithSamplingCapability() SessionOption {
	return func(s *session) {
		s.sampling = &samplingCapabilityImpl{sess: s}
	}
}

func WithRootsCapability(supportsListChanged bool) SessionOption {
	return func(s *session) {
		s.roots = &rootsCapabilityImpl{sess: s, supportsListChanged: supportsListChanged}
	}
}

func WithElicitationCapability() SessionOption {
	return func(s *session) {
		s.elicitation = &elicitationCapabilityImpl{sess: s}
	}
}

// Private concrete implementations for capabilities

type samplingCapabilityImpl struct {
	sess *session
}

func (s *samplingCapabilityImpl) CreateMessage(ctx context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	return nil, errors.New("not implemented")
}

type rootsCapabilityImpl struct {
	sess                *session
	supportsListChanged bool
}

func (r *rootsCapabilityImpl) ListRoots(ctx context.Context) (*mcp.ListRootsResult, error) {
	return nil, errors.New("not implemented")
}

func (r *rootsCapabilityImpl) RegisterRootsListChangedListener(ctx context.Context, listener RootsListChangedListener) (supported bool, err error) {
	if !r.supportsListChanged {
		return false, nil
	}

	return false, errors.New("not implemented")
}

type elicitationCapabilityImpl struct {
	sess *session
}

func (e *elicitationCapabilityImpl) Elicit(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	return nil, errors.New("not implemented")
}
