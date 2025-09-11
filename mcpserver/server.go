package mcpserver

import (
	"context"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// ServerOption configures a concrete ServerCapabilities implementation.
type ServerOption func(*server)

type server struct {
	// server info
	staticInfo   *mcp.ImplementationInfo
	infoProvider func(ctx context.Context, session sessions.Session) (mcp.ImplementationInfo, error)

	// protocol version and instructions
	staticProtocolVersion string
	protocolProvider      func(ctx context.Context, session sessions.Session) (string, bool, error)
	staticInstructions    *string
	instructionsProvider  func(ctx context.Context, session sessions.Session) (string, bool, error)

	// resources capability
	staticResourcesCap ResourcesCapability
	resourcesProvider  func(ctx context.Context, session sessions.Session) (ResourcesCapability, bool, error)

	// tools capability
	staticToolsCap ToolsCapability
	toolsProvider  func(ctx context.Context, session sessions.Session) (ToolsCapability, bool, error)
}

// NewServer builds a ServerCapabilities using functional options.
func NewServer(opts ...ServerOption) ServerCapabilities {
	s := &server{}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithServerInfo sets a static server info value.
func WithServerInfo(info mcp.ImplementationInfo) ServerOption {
	return func(s *server) { s.staticInfo = &info }
}

// WithServerInfoProvider sets a provider for per-session server info.
func WithServerInfoProvider(fn func(ctx context.Context, session sessions.Session) (mcp.ImplementationInfo, error)) ServerOption {
	return func(s *server) { s.infoProvider = fn }
}

// WithPreferredProtocolVersion sets a static preferred protocol version string.
func WithPreferredProtocolVersion(version string) ServerOption {
	return func(s *server) { s.staticProtocolVersion = version }
}

// WithPreferredProtocolVersionProvider sets a per-session provider for preferred protocol version.
func WithPreferredProtocolVersionProvider(fn func(ctx context.Context, session sessions.Session) (string, bool, error)) ServerOption {
	return func(s *server) { s.protocolProvider = fn }
}

// WithInstructions sets static human-readable instructions returned during initialize.
func WithInstructions(instr string) ServerOption {
	return func(s *server) { s.staticInstructions = &instr }
}

// WithInstructionsProvider sets a per-session provider for instructions.
func WithInstructionsProvider(fn func(ctx context.Context, session sessions.Session) (string, bool, error)) ServerOption {
	return func(s *server) { s.instructionsProvider = fn }
}

// WithResourcesCapability wires a static ResourcesCapability (used for all sessions).
func WithResourcesCapability(cap ResourcesCapability) ServerOption {
	return func(s *server) { s.staticResourcesCap = cap }
}

// WithResourcesOptions constructs a static ResourcesCapability using NewResourcesCapability.
func WithResourcesOptions(opts ...ResourcesOption) ServerOption {
	return func(s *server) { s.staticResourcesCap = NewResourcesCapability(opts...) }
}

// WithResourcesProvider wires a per-session resources capability provider.
func WithResourcesProvider(fn func(ctx context.Context, session sessions.Session) (ResourcesCapability, bool, error)) ServerOption {
	return func(s *server) { s.resourcesProvider = fn }
}

// WithToolsCapability wires a static ToolsCapability (used for all sessions).
func WithToolsCapability(cap ToolsCapability) ServerOption {
	return func(s *server) { s.staticToolsCap = cap }
}

// WithToolsOptions constructs a static ToolsCapability using NewToolsCapability.
func WithToolsOptions(opts ...ToolsOption) ServerOption {
	return func(s *server) { s.staticToolsCap = NewToolsCapability(opts...) }
}

// WithToolsProvider wires a per-session tools capability provider.
func WithToolsProvider(fn func(ctx context.Context, session sessions.Session) (ToolsCapability, bool, error)) ServerOption {
	return func(s *server) { s.toolsProvider = fn }
}

// GetServerInfo implements ServerCapabilities.
func (s *server) GetServerInfo(ctx context.Context, session sessions.Session) (mcp.ImplementationInfo, error) {
	if s.infoProvider != nil {
		return s.infoProvider(ctx, session)
	}
	if s.staticInfo != nil {
		return *s.staticInfo, nil
	}
	// Zero value if not configured; handler may still proceed.
	return mcp.ImplementationInfo{}, nil
}

// GetPreferredProtocolVersion implements ServerCapabilities.
func (s *server) GetPreferredProtocolVersion(ctx context.Context, session sessions.Session) (string, bool, error) {
	if s.protocolProvider != nil {
		return s.protocolProvider(ctx, session)
	}
	if s.staticProtocolVersion != "" {
		return s.staticProtocolVersion, true, nil
	}
	return "", false, nil
}

// GetInstructions implements ServerCapabilities.
func (s *server) GetInstructions(ctx context.Context, session sessions.Session) (string, bool, error) {
	if s.instructionsProvider != nil {
		return s.instructionsProvider(ctx, session)
	}
	if s.staticInstructions != nil {
		return *s.staticInstructions, true, nil
	}
	return "", false, nil
}

// GetResourcesCapability implements ServerCapabilities.
func (s *server) GetResourcesCapability(ctx context.Context, session sessions.Session) (ResourcesCapability, bool, error) {
	if s.resourcesProvider != nil {
		return s.resourcesProvider(ctx, session)
	}
	if s.staticResourcesCap != nil {
		return s.staticResourcesCap, true, nil
	}
	return nil, false, nil
}

// GetToolsCapability implements ServerCapabilities.
func (s *server) GetToolsCapability(ctx context.Context, session sessions.Session) (ToolsCapability, bool, error) {
	if s.toolsProvider != nil {
		return s.toolsProvider(ctx, session)
	}
	if s.staticToolsCap != nil {
		return s.staticToolsCap, true, nil
	}
	return nil, false, nil
}
