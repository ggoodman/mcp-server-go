package engine

import (
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

var _ sessions.Session = (*SessionHandle)(nil)

type SessionHandle struct {
	sessionID       string
	userID          string
	protocolVersion string

	logLevel mcp.LoggingLevel // verbosity level for this session

	// capabilities
	samplingCap    sessions.SamplingCapability
	rootsCap       sessions.RootsCapability
	elicitationCap sessions.ElicitationCapability
}

func NewSessionHandle(host sessions.SessionHost, meta *sessions.SessionMetadata, opts ...SessionHandleOption) *SessionHandle {
	s := &SessionHandle{
		sessionID:       meta.SessionID,
		userID:          meta.UserID,
		protocolVersion: meta.ProtocolVersion,

		logLevel: mcp.LoggingLevelInfo, // default
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

type SessionHandleOption func(*SessionHandle)

func WithSamplingCapability(cap sessions.SamplingCapability) SessionHandleOption {
	return func(s *SessionHandle) {
		s.samplingCap = cap
	}
}

func WithRootsCapability(cap sessions.RootsCapability) SessionHandleOption {
	return func(s *SessionHandle) {
		s.rootsCap = cap
	}
}

func WithElicitationCapability(cap sessions.ElicitationCapability) SessionHandleOption {
	return func(s *SessionHandle) {
		s.elicitationCap = cap
	}
}

func (s *SessionHandle) SessionID() string {
	return s.sessionID
}

func (s *SessionHandle) UserID() string {
	return s.userID
}

func (s *SessionHandle) ProtocolVersion() string {
	return s.protocolVersion
}

func (s *SessionHandle) GetSamplingCapability() (cap sessions.SamplingCapability, ok bool) {
	if s.samplingCap == nil {
		return nil, false
	}
	return s.samplingCap, true
}

func (s *SessionHandle) GetRootsCapability() (cap sessions.RootsCapability, ok bool) {
	if s.rootsCap == nil {
		return nil, false
	}
	return s.rootsCap, true
}

func (s *SessionHandle) GetElicitationCapability() (cap sessions.ElicitationCapability, ok bool) {
	if s.elicitationCap == nil {
		return nil, false
	}
	return s.elicitationCap, true
}
