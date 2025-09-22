package sessioncore

import (
	"github.com/ggoodman/mcp-server-go/sessions"
)

var _ sessions.Session = (*SessionHandle)(nil)

type SessionHandle struct {
}

// SessionID returns the unique session identifier.
// TODO: implement real session id plumbing
func (s *SessionHandle) SessionID() string { return "" }

// UserID returns the associated user id (if any) for the session.
// TODO: implement user identification
func (s *SessionHandle) UserID() string { return "" }

// ProtocolVersion returns the negotiated MCP protocol version.
// TODO: track negotiated version
func (s *SessionHandle) ProtocolVersion() string { return "" }

// GetSamplingCapability returns the sampling capability if present.
// TODO: wire in capability storage
func (s *SessionHandle) GetSamplingCapability() (cap sessions.SamplingCapability, ok bool) {
	return nil, false
}

// GetRootsCapability returns the roots capability if present.
// TODO: wire in capability storage
func (s *SessionHandle) GetRootsCapability() (cap sessions.RootsCapability, ok bool) {
	return nil, false
}

// GetElicitationCapability returns the elicitation capability if present.
// TODO: wire in capability storage
func (s *SessionHandle) GetElicitationCapability() (cap sessions.ElicitationCapability, ok bool) {
	return nil, false
}
