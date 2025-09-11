package sessions

import (
	"context"

	"github.com/ggoodman/mcp-streaming-http-go/broker"
)

var _ Session = (*session)(nil)

type session struct {
	id     string
	userID string

	broker broker.Broker

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
	namespace := s.sessionNamespace()
	return s.broker.Subscribe(ctx, namespace, lastEventID, func(msgCtx context.Context, envelope broker.MessageEnvelope) error {
		// Pass the envelope ID and data to the handler
		return handleMsgFn(msgCtx, envelope.ID, envelope.Data)
	})
}

func (s *session) WriteMessage(ctx context.Context, msg []byte) error {
	namespace := s.sessionNamespace()
	_, err := s.broker.Publish(ctx, namespace, msg)
	return err
}

// sessionNamespace encodes the session id into a broker namespace with a future-proof prefix.
func (s *session) sessionNamespace() string {
	return "session:" + s.id
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
