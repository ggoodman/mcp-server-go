// Package mcpservice exposes composable building blocks for implementing the
// MCP server side. Capabilities (resources, tools, prompts, etc.) are surfaced
// through minimal provider interfaces that can be satisfied by either a static
// singleton value, a container type that also acts as its own provider, or a
// small closure performing per-session selection.
//
// The unified provider pattern keeps the option surface tight (only one
// WithXCapability per capability) while supporting:
//   - Static constants: use the StaticX helper (e.g. StaticProtocolVersion("2025-06-18"))
//   - Self-providing containers: pass *ToolsContainer / *ResourcesContainer directly
//   - Dynamic logic: supply an XProviderFunc that inspects session metadata
//
// Providers return (value, ok, error). ok == false means “capability absent”.
// An empty value with ok == true is still advertised (e.g. empty tools list).
package mcpservice

import (
	"context"
	"fmt"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// Provider interfaces & adapter function types. Each returns (value, ok, error)
// where ok distinguishes absence (false) from presence (true) even if the
// underlying value may be empty.

// ServerInfoProvider yields implementation metadata. Typically static; use a
// function provider only if you need tenant‑specific branding.
type ServerInfoProvider interface {
	ProvideServerInfo(ctx context.Context, session sessions.Session) (mcp.ImplementationInfo, bool, error)
}
type ServerInfoProviderFunc func(ctx context.Context, session sessions.Session) (mcp.ImplementationInfo, bool, error)

func (f ServerInfoProviderFunc) ProvideServerInfo(ctx context.Context, s sessions.Session) (mcp.ImplementationInfo, bool, error) {
	return f(ctx, s)
}

// ProtocolVersionProvider yields a preferred protocol version given the
// client's advertised protocol version.
//
// Note: protocol version selection occurs during initialize, before a session
// is created. As a result, session may be nil and implementations MUST NOT
// assume it is non-nil.
type ProtocolVersionProvider interface {
	ProvideProtocolVersion(ctx context.Context, session sessions.Session, clientProtocolVersion string) (string, bool, error)
}

// ProtocolVersionProviderFunc adapts a function to a ProtocolVersionProvider.
type ProtocolVersionProviderFunc func(ctx context.Context, session sessions.Session, clientProtocolVersion string) (string, bool, error)

func (f ProtocolVersionProviderFunc) ProvideProtocolVersion(ctx context.Context, s sessions.Session, clientProtocolVersion string) (string, bool, error) {
	return f(ctx, s, clientProtocolVersion)
}

// InstructionsProvider supplies optional human-readable instructions returned
// in initialize. Return ok=false to omit the field.
type InstructionsProvider interface {
	ProvideInstructions(ctx context.Context, session sessions.Session) (string, bool, error)
}
type InstructionsProviderFunc func(ctx context.Context, session sessions.Session) (string, bool, error)

func (f InstructionsProviderFunc) ProvideInstructions(ctx context.Context, s sessions.Session) (string, bool, error) {
	return f(ctx, s)
}

// ResourcesCapabilityProvider yields a ResourcesCapability (list/read). Use a
// provider func for per-session ACL or tenant scoping.
type ResourcesCapabilityProvider interface {
	ProvideResources(ctx context.Context, session sessions.Session) (ResourcesCapability, bool, error)
}
type ResourcesCapabilityProviderFunc func(ctx context.Context, session sessions.Session) (ResourcesCapability, bool, error)

func (f ResourcesCapabilityProviderFunc) ProvideResources(ctx context.Context, s sessions.Session) (ResourcesCapability, bool, error) {
	return f(ctx, s)
}

// ToolsCapabilityProvider yields a ToolsCapability (list + invoke). ok=false
// suppresses the entire capability.
type ToolsCapabilityProvider interface {
	ProvideTools(ctx context.Context, session sessions.Session) (ToolsCapability, bool, error)
}
type ToolsCapabilityProviderFunc func(ctx context.Context, session sessions.Session) (ToolsCapability, bool, error)

func (f ToolsCapabilityProviderFunc) ProvideTools(ctx context.Context, s sessions.Session) (ToolsCapability, bool, error) {
	return f(ctx, s)
}

// PromptsCapabilityProvider yields a PromptsCapability (named prompt templates).
type PromptsCapabilityProvider interface {
	ProvidePrompts(ctx context.Context, session sessions.Session) (PromptsCapability, bool, error)
}
type PromptsCapabilityProviderFunc func(ctx context.Context, session sessions.Session) (PromptsCapability, bool, error)

func (f PromptsCapabilityProviderFunc) ProvidePrompts(ctx context.Context, s sessions.Session) (PromptsCapability, bool, error) {
	return f(ctx, s)
}

// LoggingCapabilityProvider yields logging/setLevel support.
type LoggingCapabilityProvider interface {
	ProvideLogging(ctx context.Context, session sessions.Session) (LoggingCapability, bool, error)
}
type LoggingCapabilityProviderFunc func(ctx context.Context, session sessions.Session) (LoggingCapability, bool, error)

func (f LoggingCapabilityProviderFunc) ProvideLogging(ctx context.Context, s sessions.Session) (LoggingCapability, bool, error) {
	return f(ctx, s)
}

// CompletionsCapabilityProvider yields the (experimental) completions capability.
type CompletionsCapabilityProvider interface {
	ProvideCompletions(ctx context.Context, session sessions.Session) (CompletionsCapability, bool, error)
}
type CompletionsCapabilityProviderFunc func(ctx context.Context, session sessions.Session) (CompletionsCapability, bool, error)

func (f CompletionsCapabilityProviderFunc) ProvideCompletions(ctx context.Context, s sessions.Session) (CompletionsCapability, bool, error) {
	return f(ctx, s)
}

// Static helper constructors (ergonomic wrappers)
// ServerInfoOption configures optional fields on the server's implementation info.
type ServerInfoOption func(*mcp.ImplementationInfo)

// WithServerInfoTitle sets the optional human friendly title.
func WithServerInfoTitle(title string) ServerInfoOption {
	return func(info *mcp.ImplementationInfo) { info.Title = title }
}

// StaticServerInfo returns a provider that always supplies the same implementation
// info. Use WithServerInfoTitle for an optional human friendly title.
func StaticServerInfo(name, version string, opts ...ServerInfoOption) ServerInfoProvider {
	info := mcp.ImplementationInfo{Name: name, Version: version}
	for _, opt := range opts {
		if opt != nil {
			opt(&info)
		}
	}
	return ServerInfoProviderFunc(func(context.Context, sessions.Session) (mcp.ImplementationInfo, bool, error) { return info, true, nil })
}
func StaticProtocolVersion(v string) ProtocolVersionProvider {
	if v == "" {
		return ProtocolVersionProviderFunc(func(context.Context, sessions.Session, string) (string, bool, error) { return "", false, nil })
	}
	if !mcp.IsSupportedProtocolVersion(v) {
		return ProtocolVersionProviderFunc(func(context.Context, sessions.Session, string) (string, bool, error) {
			return "", false, fmt.Errorf("unsupported protocol version %q", v)
		})
	}
	return ProtocolVersionProviderFunc(func(context.Context, sessions.Session, string) (string, bool, error) { return v, true, nil })
}
func StaticInstructions(s string) InstructionsProvider {
	return InstructionsProviderFunc(func(context.Context, sessions.Session) (string, bool, error) { return s, s != "", nil })
}
func StaticResources(cap ResourcesCapability) ResourcesCapabilityProvider {
	if cap == nil {
		return ResourcesCapabilityProviderFunc(func(context.Context, sessions.Session) (ResourcesCapability, bool, error) { return nil, false, nil })
	}
	return ResourcesCapabilityProviderFunc(func(context.Context, sessions.Session) (ResourcesCapability, bool, error) { return cap, true, nil })
}
func StaticTools(cap ToolsCapability) ToolsCapabilityProvider {
	if cap == nil {
		return ToolsCapabilityProviderFunc(func(context.Context, sessions.Session) (ToolsCapability, bool, error) { return nil, false, nil })
	}
	return ToolsCapabilityProviderFunc(func(context.Context, sessions.Session) (ToolsCapability, bool, error) { return cap, true, nil })
}
func StaticPrompts(cap PromptsCapability) PromptsCapabilityProvider {
	if cap == nil {
		return PromptsCapabilityProviderFunc(func(context.Context, sessions.Session) (PromptsCapability, bool, error) { return nil, false, nil })
	}
	return PromptsCapabilityProviderFunc(func(context.Context, sessions.Session) (PromptsCapability, bool, error) { return cap, true, nil })
}
func StaticLogging(cap LoggingCapability) LoggingCapabilityProvider {
	if cap == nil {
		return LoggingCapabilityProviderFunc(func(context.Context, sessions.Session) (LoggingCapability, bool, error) { return nil, false, nil })
	}
	return LoggingCapabilityProviderFunc(func(context.Context, sessions.Session) (LoggingCapability, bool, error) { return cap, true, nil })
}
func StaticCompletions(cap CompletionsCapability) CompletionsCapabilityProvider {
	if cap == nil {
		return CompletionsCapabilityProviderFunc(func(context.Context, sessions.Session) (CompletionsCapability, bool, error) { return nil, false, nil })
	}
	return CompletionsCapabilityProviderFunc(func(context.Context, sessions.Session) (CompletionsCapability, bool, error) { return cap, true, nil })
}
