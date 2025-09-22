package mcpservice

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
	protocolProvider      func(ctx context.Context) (string, bool, error)
	staticInstructions    *string
	instructionsProvider  func(ctx context.Context, session sessions.Session) (string, bool, error)

	// resources capability
	staticResourcesCap ResourcesCapability
	resourcesProvider  func(ctx context.Context, session sessions.Session) (ResourcesCapability, bool, error)

	// tools capability
	staticToolsCap ToolsCapability
	toolsProvider  func(ctx context.Context, session sessions.Session) (ToolsCapability, bool, error)

	// prompts capability
	staticPromptsCap PromptsCapability
	promptsProvider  func(ctx context.Context, session sessions.Session) (PromptsCapability, bool, error)

	// logging capability
	staticLoggingCap LoggingCapability
	loggingProvider  func(ctx context.Context, session sessions.Session) (LoggingCapability, bool, error)

	// completions capability
	staticCompletionsCap CompletionsCapability
	completionsProvider  func(ctx context.Context, session sessions.Session) (CompletionsCapability, bool, error)
}

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
func WithPreferredProtocolVersionProvider(fn func(ctx context.Context) (string, bool, error)) ServerOption {
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

// WithPromptsCapability wires a static PromptsCapability (used for all sessions).
func WithPromptsCapability(cap PromptsCapability) ServerOption {
	return func(s *server) { s.staticPromptsCap = cap }
}

// WithPromptsOptions constructs a static PromptsCapability using NewPromptsCapability.
func WithPromptsOptions(opts ...PromptsOption) ServerOption {
	return func(s *server) { s.staticPromptsCap = NewPromptsCapability(opts...) }
}

// WithPromptsProvider wires a per-session prompts capability provider.
func WithPromptsProvider(fn func(ctx context.Context, session sessions.Session) (PromptsCapability, bool, error)) ServerOption {
	return func(s *server) { s.promptsProvider = fn }
}

// WithLoggingCapability wires a static LoggingCapability (used for all sessions).
func WithLoggingCapability(cap LoggingCapability) ServerOption {
	return func(s *server) { s.staticLoggingCap = cap }
}

// WithLoggingProvider wires a per-session logging capability provider.
func WithLoggingProvider(fn func(ctx context.Context, session sessions.Session) (LoggingCapability, bool, error)) ServerOption {
	return func(s *server) { s.loggingProvider = fn }
}

// WithCompletionsCapability wires a static CompletionsCapability (used for all sessions).
func WithCompletionsCapability(cap CompletionsCapability) ServerOption {
	return func(s *server) { s.staticCompletionsCap = cap }
}

// WithCompletionsProvider wires a per-session completions capability provider.
func WithCompletionsProvider(fn func(ctx context.Context, session sessions.Session) (CompletionsCapability, bool, error)) ServerOption {
	return func(s *server) { s.completionsProvider = fn }
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
func (s *server) GetPreferredProtocolVersion(ctx context.Context) (string, bool, error) {
	if s.protocolProvider != nil {
		return s.protocolProvider(ctx)
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

// GetPromptsCapability implements ServerCapabilities.
func (s *server) GetPromptsCapability(ctx context.Context, session sessions.Session) (PromptsCapability, bool, error) {
	if s.promptsProvider != nil {
		return s.promptsProvider(ctx, session)
	}
	if s.staticPromptsCap != nil {
		return s.staticPromptsCap, true, nil
	}
	return nil, false, nil
}

// GetLoggingCapability implements ServerCapabilities.
func (s *server) GetLoggingCapability(ctx context.Context, session sessions.Session) (LoggingCapability, bool, error) {
	if s.loggingProvider != nil {
		return s.loggingProvider(ctx, session)
	}
	if s.staticLoggingCap != nil {
		return s.staticLoggingCap, true, nil
	}
	return nil, false, nil
}

// GetCompletionsCapability implements ServerCapabilities.
func (s *server) GetCompletionsCapability(ctx context.Context, session sessions.Session) (CompletionsCapability, bool, error) {
	if s.completionsProvider != nil {
		return s.completionsProvider(ctx, session)
	}
	if s.staticCompletionsCap != nil {
		return s.staticCompletionsCap, true, nil
	}
	return nil, false, nil
}
