package mcpserver

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// Callback signatures for dynamic behavior.
type (
	ListToolsFunc func(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Tool], error)
	CallToolFunc  func(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error)
)

// Functional options for configuring the tools capability.
type ToolsOption func(*toolsCapability)

type toolsCapability struct {
	// Optional dynamic behavior
	listToolsFn ListToolsFunc
	callToolFn  CallToolFunc

	// Optional static tools container
	staticContainer *StaticTools

	// Static paging
	pageSize int

	// Optional change notification subscriber (process-local)
	changeSub ChangeSubscriber
}

// NewToolsCapability constructs a ToolsCapability using the provided options.
// It supports both static and dynamic modes via functional options.
func NewToolsCapability(opts ...ToolsOption) ToolsCapability {
	tc := &toolsCapability{
		pageSize: 50,
	}
	for _, opt := range opts {
		opt(tc)
	}
	return tc
}

// WithListTools sets a custom list-tools function.
func WithListTools(fn ListToolsFunc) ToolsOption {
	return func(tc *toolsCapability) { tc.listToolsFn = fn }
}

// WithCallTool sets a custom call-tool function.
func WithCallTool(fn CallToolFunc) ToolsOption {
	return func(tc *toolsCapability) { tc.callToolFn = fn }
}

// WithStaticToolsContainer provides a static tool container to advertise.
// When present, the capability will automatically advertise listChanged support.
func WithStaticToolsContainer(st *StaticTools) ToolsOption {
	return func(tc *toolsCapability) { tc.staticContainer = st }
}

// WithToolsPageSize sets the page size for static pagination.
func WithToolsPageSize(n int) ToolsOption {
	return func(tc *toolsCapability) {
		if n > 0 {
			tc.pageSize = n
		}
	}
}

// WithToolsChangeNotification wires a ChangeSubscriber to enable list-changed notifications.
func WithToolsChangeNotification(sub ChangeSubscriber) ToolsOption {
	return func(tc *toolsCapability) { tc.changeSub = sub }
}

// ListTools implements ToolsCapability.
func (tc *toolsCapability) ListTools(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Tool], error) {
	if tc.listToolsFn != nil {
		return tc.listToolsFn(ctx, session, cursor)
	}
	if tc.staticContainer != nil {
		return tc.pageTools(tc.staticContainer.Snapshot(), cursor), nil
	}
	return NewPage[mcp.Tool](nil), nil
}

// CallTool implements ToolsCapability.
func (tc *toolsCapability) CallTool(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
	if req == nil || req.Name == "" {
		return nil, fmt.Errorf("invalid tool request: missing name")
	}
	// Explicit override wins.
	if tc.callToolFn != nil {
		return tc.callToolFn(ctx, session, req)
	}
	// In static mode, delegate to the container's dispatcher if available.
	if tc.staticContainer != nil {
		return tc.staticContainer.Call(ctx, session, req)
	}
	return nil, fmt.Errorf("tool not found: %s", req.Name)
}

// GetListChangedCapability advertises list-changed support when a change subscriber is configured.
func (tc *toolsCapability) GetListChangedCapability(ctx context.Context, session sessions.Session) (ToolListChangedCapability, bool, error) {
	// If a static container is present, we automatically expose listChanged support.
	if tc.staticContainer != nil {
		return toolsListChangedFromSubscriber{tc: tc}, true, nil
	}
	if tc.changeSub != nil {
		return toolsListChangedFromSubscriber{tc: tc}, true, nil
	}
	return nil, false, nil
}

// Helper: paginate static tools
func (tc *toolsCapability) pageTools(all []mcp.Tool, cursor *string) Page[mcp.Tool] {
	start := parseCursor(cursor)
	if start < 0 || start > len(all) {
		start = 0
	}
	end := start + tc.pageSize
	if end > len(all) {
		end = len(all)
	}
	items := make([]mcp.Tool, end-start)
	copy(items, all[start:end])
	if end < len(all) {
		next := strconv.Itoa(end)
		return NewPage(items, WithNextCursor[mcp.Tool](next))
	}
	return NewPage(items)
}

// toolsListChangedFromSubscriber adapts a ChangeSubscriber to ToolListChangedCapability.
type toolsListChangedFromSubscriber struct{ tc *toolsCapability }

func (t toolsListChangedFromSubscriber) Register(ctx context.Context, session sessions.Session, fn NotifyToolsListChangedFunc) (bool, error) {
	if t.tc == nil || fn == nil {
		return false, nil
	}
	var ch <-chan struct{}
	if t.tc.staticContainer != nil {
		ch = t.tc.staticContainer.Subscriber()
	} else if t.tc.changeSub != nil {
		ch = t.tc.changeSub.Subscriber()
	} else {
		return false, nil
	}
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
