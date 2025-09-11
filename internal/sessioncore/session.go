package sessioncore

import (
	"context"

	"github.com/ggoodman/mcp-server-go/sessions"
)

type SessionHandle struct {
	id     string
	userID string

	backend sessions.SessionHost

	sampling        sessions.SamplingCapability
	roots           sessions.RootsCapability
	elicitation     sessions.ElicitationCapability
	protocolVersion string
}

func (s *SessionHandle) SessionID() string {
	return s.id
}

func (s *SessionHandle) UserID() string {
	return s.userID
}

func (s *SessionHandle) ConsumeMessages(ctx context.Context, lastEventID string, handleMsgFn sessions.MessageHandlerFunction) error {
	return s.backend.SubscribeSession(ctx, s.id, lastEventID, handleMsgFn)
}

func (s *SessionHandle) WriteMessage(ctx context.Context, msg []byte) error {
	_, err := s.backend.PublishSession(ctx, s.id, msg)
	return err
}

func (s *SessionHandle) Session() sessions.Session {
	return &userSession{h: s}
}

// userSession is the user-facing implementation of sessions.Session, backed by a SessionHandle.
type userSession struct {
	h *SessionHandle
}

func (u *userSession) SessionID() string       { return u.h.id }
func (u *userSession) UserID() string          { return u.h.userID }
func (u *userSession) ProtocolVersion() string { return u.h.protocolVersion }

func (u *userSession) GetSamplingCapability() (sessions.SamplingCapability, bool) {
	if u.h.sampling == nil {
		return nil, false
	}
	return u.h.sampling, true
}

func (u *userSession) GetRootsCapability() (sessions.RootsCapability, bool) {
	if u.h.roots == nil {
		return nil, false
	}
	return u.h.roots, true
}

func (u *userSession) GetElicitationCapability() (sessions.ElicitationCapability, bool) {
	if u.h.elicitation == nil {
		return nil, false
	}
	return u.h.elicitation, true
}
