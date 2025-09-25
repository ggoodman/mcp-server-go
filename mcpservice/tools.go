package mcpservice

import (
	"context"
	"fmt"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// Dynamic constructor / option set for tools capability.
// Static tools now implement ToolsCapability directly (see static_tools.go).

// ListToolsFunc returns a (possibly paginated) page of tools for the session.
// The function MUST honor the context for cancellation.
type ListToolsFunc func(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Tool], error)

// CallToolFunc executes a tool invocation.
type CallToolFunc func(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error)

// DynamicToolsOption configures a dynamically implemented tools capability.
type DynamicToolsOption func(*dynamicTools)

// dynamicTools is the dynamic (function-backed) implementation of ToolsCapability.
// It does NOT do paging itself beyond returning whatever ListFn supplies; callers
// that want internal paging should implement it inside ListFn.
type dynamicTools struct {
	listFn    ListToolsFunc
	callFn    CallToolFunc
	changeSub ChangeSubscriber
}

// NewDynamicTools builds a dynamic tools capability from option functions.
// If listFn is nil, ListTools returns an empty page. If callFn is nil, tool calls
// result in a not-found error. changeSub enables listChanged notifications.
func NewDynamicTools(opts ...DynamicToolsOption) ToolsCapability {
	dt := &dynamicTools{}
	for _, opt := range opts {
		opt(dt)
	}
	return dt
}

// WithToolsListFn sets the listing function for a dynamic tools capability.
func WithToolsListFn(fn ListToolsFunc) DynamicToolsOption {
	return func(d *dynamicTools) { d.listFn = fn }
}

// WithToolsCallFn sets the call function for a dynamic tools capability.
func WithToolsCallFn(fn CallToolFunc) DynamicToolsOption {
	return func(d *dynamicTools) { d.callFn = fn }
}

// WithToolsChangeSubscriber wires a ChangeSubscriber for listChanged notifications.
func WithToolsChangeSubscriber(sub ChangeSubscriber) DynamicToolsOption {
	return func(d *dynamicTools) { d.changeSub = sub }
}

// ListTools implements ToolsCapability for dynamic tools.
func (d *dynamicTools) ListTools(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Tool], error) {
	if d.listFn == nil {
		return NewPage[mcp.Tool](nil), nil
	}
	return d.listFn(ctx, session, cursor)
}

// CallTool implements ToolsCapability for dynamic tools.
func (d *dynamicTools) CallTool(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
	if req == nil || req.Name == "" {
		return nil, fmt.Errorf("invalid tool request: missing name")
	}
	if d.callFn == nil {
		return nil, fmt.Errorf("tool not found: %s", req.Name)
	}
	return d.callFn(ctx, session, req)
}

// GetListChangedCapability implements ToolsCapability listChanged advertisement.
func (d *dynamicTools) GetListChangedCapability(ctx context.Context, session sessions.Session) (ToolListChangedCapability, bool, error) {
	if d.changeSub == nil {
		return nil, false, nil
	}
	return toolsListChangedFromSubscriber{sub: d.changeSub}, true, nil
}

// toolsListChangedFromSubscriber adapts a ChangeSubscriber to ToolListChangedCapability.
type toolsListChangedFromSubscriber struct{ sub ChangeSubscriber }

func (t toolsListChangedFromSubscriber) Register(ctx context.Context, session sessions.Session, fn NotifyToolsListChangedFunc) (bool, error) {
	if t.sub == nil || fn == nil {
		return false, nil
	}
	ch := t.sub.Subscriber()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				fn(ctx, session)
			}
		}
	}()
	return true, nil
}
