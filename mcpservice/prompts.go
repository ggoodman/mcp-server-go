package mcpservice

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// Callback signatures for dynamic behavior.
// --- New Prompts refactor: static container implements PromptsCapability; dynamic version via constructor ---

type (
	ListPromptsFunc func(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Prompt], error)
	GetPromptFunc   func(ctx context.Context, session sessions.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error)
)

// Dynamic prompts implementation
type dynamicPrompts struct {
	listFn ListPromptsFunc
	getFn  GetPromptFunc
	change ChangeSubscriber
}

type DynamicPromptsOption func(*dynamicPrompts)

func NewDynamicPrompts(opts ...DynamicPromptsOption) PromptsCapability {
	dp := &dynamicPrompts{}
	for _, o := range opts {
		o(dp)
	}
	return dp
}

func WithPromptsListFunc(fn ListPromptsFunc) DynamicPromptsOption {
	return func(d *dynamicPrompts) { d.listFn = fn }
}
func WithPromptsGetFunc(fn GetPromptFunc) DynamicPromptsOption {
	return func(d *dynamicPrompts) { d.getFn = fn }
}
func WithPromptsChangeSubscriber(sub ChangeSubscriber) DynamicPromptsOption {
	return func(d *dynamicPrompts) { d.change = sub }
}

// dynamicPrompts implements PromptsCapability
func (d *dynamicPrompts) ListPrompts(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Prompt], error) {
	if d.listFn == nil {
		return NewPage[mcp.Prompt](nil), nil
	}
	return d.listFn(ctx, session, cursor)
}
func (d *dynamicPrompts) GetPrompt(ctx context.Context, session sessions.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error) {
	if req == nil || req.Name == "" {
		return nil, fmt.Errorf("invalid prompt request: missing name")
	}
	if d.getFn == nil {
		return nil, fmt.Errorf("prompt not found: %s", req.Name)
	}
	return d.getFn(ctx, session, req)
}
func (d *dynamicPrompts) GetListChangedCapability(ctx context.Context, session sessions.Session) (PromptListChangedCapability, bool, error) {
	if d.change == nil {
		return nil, false, nil
	}
	return promptsListChangedFromSubscriber{sub: d.change}, true, nil
}

// PromptsContainer now implements PromptsCapability directly.

// ListPrompts implements PromptsCapability for PromptsContainer
func (sp *PromptsContainer) ListPrompts(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Prompt], error) {
	all := sp.Snapshot()
	// reuse pagination logic inline (page size 50 for now; could add SetPageSize later if needed)
	start := parseCursor(cursor)
	if start < 0 || start > len(all) {
		start = 0
	}
	pageSize := 50
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	items := make([]mcp.Prompt, end-start)
	copy(items, all[start:end])
	if end < len(all) {
		return NewPage(items, WithNextCursor[mcp.Prompt](strconv.Itoa(end))), nil
	}
	return NewPage(items), nil
}

// GetPrompt implements PromptsCapability for PromptsContainer
func (sp *PromptsContainer) GetPrompt(ctx context.Context, session sessions.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error) {
	return sp.Get(ctx, session, req)
}

// GetListChangedCapability implements PromptsCapability for PromptsContainer
func (sp *PromptsContainer) GetListChangedCapability(ctx context.Context, session sessions.Session) (PromptListChangedCapability, bool, error) {
	return promptsListChangedFromSubscriber{sub: sp}, true, nil
}

type promptsListChangedFromSubscriber struct{ sub ChangeSubscriber }

func (p promptsListChangedFromSubscriber) Register(ctx context.Context, session sessions.Session, fn NotifyPromptsListChangedFunc) (bool, error) {
	if p.sub == nil || fn == nil {
		return false, nil
	}
	ch := p.sub.Subscriber()
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
