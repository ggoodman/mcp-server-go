package mcpservice

import (
	"context"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// ServerOption configures a concrete ServerCapabilities implementation.
type ServerOption func(*server)

// server holds only provider interfaces (no duplicate static fields). A single
// pattern: each WithXCapability takes an XCapabilityProvider. Static values use
// StaticX helpers; dynamic cases use XProviderFunc.
type server struct {
	infoProv         ServerInfoProvider
	protocolProv     ProtocolVersionProvider
	instructionsProv InstructionsProvider
	resourcesProv    ResourcesCapabilityProvider
	toolsProv        ToolsCapabilityProvider
	promptsProv      PromptsCapabilityProvider
	loggingProv      LoggingCapabilityProvider
	completionsProv  CompletionsCapabilityProvider
}

// Provider interfaces and static helper constructors have moved to providers.go for clarity.

// NewServer builds a ServerCapabilities using functional options. Options allow
// configuring static fields or per-session providers for info, protocol
// preference, instructions, resources and tools.
func NewServer(opts ...ServerOption) ServerCapabilities {
	s := &server{}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithServerInfo sets a provider for server info (usually StaticServerInfo).
func WithServerInfo(p ServerInfoProvider) ServerOption { return func(s *server) { s.infoProv = p } }

// WithProtocolVersion sets a provider for the preferred protocol version.
func WithProtocolVersion(p ProtocolVersionProvider) ServerOption {
	return func(s *server) { s.protocolProv = p }
}

// WithInstructions sets a provider for human-readable instructions returned
// during initialize.
func WithInstructions(p InstructionsProvider) ServerOption {
	return func(s *server) { s.instructionsProv = p }
}

// WithResourcesCapability wires the resources capability provider.
func WithResourcesCapability(p ResourcesCapabilityProvider) ServerOption {
	return func(s *server) { s.resourcesProv = p }
}

// WithToolsCapability wires the tools capability provider.
func WithToolsCapability(p ToolsCapabilityProvider) ServerOption {
	return func(s *server) { s.toolsProv = p }
}

// WithPromptsCapability wires the prompts capability provider.
func WithPromptsCapability(p PromptsCapabilityProvider) ServerOption {
	return func(s *server) { s.promptsProv = p }
}

// WithLoggingCapability wires the logging capability provider.
func WithLoggingCapability(p LoggingCapabilityProvider) ServerOption {
	return func(s *server) { s.loggingProv = p }
}

// WithCompletionsCapability wires the completions capability provider.
func WithCompletionsCapability(p CompletionsCapabilityProvider) ServerOption {
	return func(s *server) { s.completionsProv = p }
}

// GetServerInfo implements ServerCapabilities.
func (s *server) GetServerInfo(ctx context.Context, session sessions.Session) (mcp.ImplementationInfo, error) {
	if s.infoProv == nil {
		return mcp.ImplementationInfo{}, nil
	}
	info, ok, err := s.infoProv.ProvideServerInfo(ctx, session)
	if err != nil || !ok {
		return mcp.ImplementationInfo{}, err
	}
	return info, nil
}

// GetPreferredProtocolVersion implements ServerCapabilities.
func (s *server) GetPreferredProtocolVersion(ctx context.Context, clientProtocolVersion string) (string, bool, error) {
	if s.protocolProv == nil {
		return "", false, nil
	}
	// Pass a nil session; protocol currently session-agnostic. Could supply a dummy.
	return s.protocolProv.ProvideProtocolVersion(ctx, nil, clientProtocolVersion)
}

// GetInstructions implements ServerCapabilities.
func (s *server) GetInstructions(ctx context.Context, session sessions.Session) (string, bool, error) {
	if s.instructionsProv == nil {
		return "", false, nil
	}
	return s.instructionsProv.ProvideInstructions(ctx, session)
}

// GetResourcesCapability implements ServerCapabilities.
func (s *server) GetResourcesCapability(ctx context.Context, session sessions.Session) (ResourcesCapability, bool, error) {
	if s.resourcesProv == nil {
		return nil, false, nil
	}
	return s.resourcesProv.ProvideResources(ctx, session)
}

// GetToolsCapability implements ServerCapabilities.
func (s *server) GetToolsCapability(ctx context.Context, session sessions.Session) (ToolsCapability, bool, error) {
	if s.toolsProv == nil {
		return nil, false, nil
	}
	return s.toolsProv.ProvideTools(ctx, session)
}

// GetPromptsCapability implements ServerCapabilities.
func (s *server) GetPromptsCapability(ctx context.Context, session sessions.Session) (PromptsCapability, bool, error) {
	if s.promptsProv == nil {
		return nil, false, nil
	}
	return s.promptsProv.ProvidePrompts(ctx, session)
}

// GetLoggingCapability implements ServerCapabilities.
func (s *server) GetLoggingCapability(ctx context.Context, session sessions.Session) (LoggingCapability, bool, error) {
	if s.loggingProv == nil {
		return nil, false, nil
	}
	return s.loggingProv.ProvideLogging(ctx, session)
}

// GetCompletionsCapability implements ServerCapabilities.
func (s *server) GetCompletionsCapability(ctx context.Context, session sessions.Session) (CompletionsCapability, bool, error) {
	if s.completionsProv == nil {
		return nil, false, nil
	}
	return s.completionsProv.ProvideCompletions(ctx, session)
}
