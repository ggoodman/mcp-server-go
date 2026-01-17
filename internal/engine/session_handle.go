package engine

import (
	"context"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

var _ sessions.Session = (*SessionHandle)(nil)

type SessionHandle struct {
	host                  sessions.SessionHost
	sessionID             string
	userID                string
	clientProtocolVersion string
	serverProtocolVersion string
	state                 sessions.SessionState

	logLevel mcp.LoggingLevel // verbosity level for this session

	// capabilities
	samplingCap    sessions.SamplingCapability
	rootsCap       sessions.RootsCapability
	elicitationCap sessions.ElicitationCapability
}

func NewSessionHandle(host sessions.SessionHost, meta *sessions.SessionMetadata, opts ...SessionHandleOption) *SessionHandle {
	s := &SessionHandle{
		host:                  host,
		sessionID:             meta.SessionID,
		userID:                meta.UserID,
		clientProtocolVersion: meta.ClientProtocolVersion,
		serverProtocolVersion: meta.ServerProtocolVersion,
		state:                 meta.State,

		logLevel: mcp.LoggingLevelInfo, // default
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

func (s *SessionHandle) State() sessions.SessionState { return s.state }

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

func (s *SessionHandle) ClientProtocolVersion() string {
	return s.clientProtocolVersion
}

func (s *SessionHandle) ServerProtocolVersion() string {
	return s.serverProtocolVersion
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

// PutData stores raw bytes for this session key via the SessionHost.
func (s *SessionHandle) PutData(ctx context.Context, key string, value []byte) error {
	return s.host.PutSessionData(ctx, s.sessionID, key, value)
}

// GetData retrieves raw bytes for this session key. Returns ok=false if absent.
func (s *SessionHandle) GetData(ctx context.Context, key string) ([]byte, bool, error) {
	b, ok, err := s.host.GetSessionData(ctx, s.sessionID, key)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return b, true, nil
}

// DeleteData removes the key (idempotent).
func (s *SessionHandle) DeleteData(ctx context.Context, key string) error {
	return s.host.DeleteSessionData(ctx, s.sessionID, key)
}

// isNotFoundErr attempts to classify a storage-layer not-found error. We keep
// it conservative; if unsure, treat as real error so callers can act.
// (not-found classification helper removed; SessionHost now returns explicit found bool)
