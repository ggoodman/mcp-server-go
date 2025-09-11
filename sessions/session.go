package sessions

import (
	"context"
)

var _ Session = (*session)(nil)

type session struct {
	id     string
	userID string

	backend SessionHost

	sampling    SamplingCapability
	roots       RootsCapability
	elicitation ElicitationCapability
}

func (s *session) SessionID() string {
	return s.id
}

func (s *session) UserID() string {
	return s.userID
}

func (s *session) ConsumeMessages(ctx context.Context, lastEventID string, handleMsgFn MessageHandlerFunction) error {
	return s.backend.SubscribeSession(ctx, s.id, lastEventID, handleMsgFn)
}

func (s *session) WriteMessage(ctx context.Context, msg []byte) error {
	_, err := s.backend.PublishSession(ctx, s.id, msg)
	return err
}

func (s *session) GetSamplingCapability() (SamplingCapability, bool) {
	if s.sampling == nil {
		return nil, false
	}
	return s.sampling, true
}

func (s *session) GetRootsCapability() (RootsCapability, bool) {
	if s.roots == nil {
		return nil, false
	}
	return s.roots, true
}

func (s *session) GetElicitationCapability() (ElicitationCapability, bool) {
	if s.elicitation == nil {
		return nil, false
	}
	return s.elicitation, true
}
