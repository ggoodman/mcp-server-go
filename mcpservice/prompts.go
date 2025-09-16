package mcpservice

import (
    "context"
    "fmt"
    "strconv"

    "github.com/ggoodman/mcp-server-go/mcp"
    "github.com/ggoodman/mcp-server-go/sessions"
)

// Callback signatures for dynamic behavior.
type (
    ListPromptsFunc func(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Prompt], error)
    GetPromptFunc   func(ctx context.Context, session sessions.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error)
)

// PromptsOption is a functional option for configuring the prompts capability.
type PromptsOption func(*promptsCapability)

type promptsCapability struct {
    // Optional dynamic behavior
    listPromptsFn ListPromptsFunc
    getPromptFn   GetPromptFunc

    // Optional static prompts container
    staticContainer *StaticPrompts

    // Static paging
    pageSize int

    // Optional change notification subscriber (process-local)
    changeSub ChangeSubscriber
}

// NewPromptsCapability constructs a PromptsCapability using the provided options.
// It supports both static and dynamic modes via functional options.
func NewPromptsCapability(opts ...PromptsOption) PromptsCapability {
    pc := &promptsCapability{pageSize: 50}
    for _, opt := range opts {
        opt(pc)
    }
    return pc
}

// WithStaticPromptsContainer provides a static prompts container to advertise.
// When present, the capability will automatically advertise listChanged support.
func WithStaticPromptsContainer(sp *StaticPrompts) PromptsOption {
    return func(pc *promptsCapability) {
        pc.staticContainer = sp
        pc.changeSub = sp
    }
}

// WithListPrompts sets a custom list-prompts function.
func WithListPrompts(fn ListPromptsFunc) PromptsOption {
    return func(pc *promptsCapability) { pc.listPromptsFn = fn }
}

// WithGetPrompt sets a custom get-prompt function.
func WithGetPrompt(fn GetPromptFunc) PromptsOption {
    return func(pc *promptsCapability) { pc.getPromptFn = fn }
}

// WithPromptsPageSize sets the page size for static pagination.
func WithPromptsPageSize(n int) PromptsOption {
    return func(pc *promptsCapability) {
        if n > 0 {
            pc.pageSize = n
        }
    }
}

// WithPromptsChangeNotification wires a ChangeSubscriber to enable list-changed notifications.
func WithPromptsChangeNotification(sub ChangeSubscriber) PromptsOption {
    return func(pc *promptsCapability) { pc.changeSub = sub }
}

// ListPrompts implements PromptsCapability.
func (pc *promptsCapability) ListPrompts(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Prompt], error) {
    if pc.listPromptsFn != nil {
        return pc.listPromptsFn(ctx, session, cursor)
    }
    if pc.staticContainer != nil {
        return pc.pagePrompts(pc.staticContainer.Snapshot(), cursor), nil
    }
    return NewPage[mcp.Prompt](nil), nil
}

// GetPrompt implements PromptsCapability.
func (pc *promptsCapability) GetPrompt(ctx context.Context, session sessions.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error) {
    if req == nil || req.Name == "" {
        return nil, fmt.Errorf("invalid prompt request: missing name")
    }
    if pc.getPromptFn != nil {
        return pc.getPromptFn(ctx, session, req)
    }
    if pc.staticContainer != nil {
        return pc.staticContainer.Get(ctx, session, req)
    }
    return nil, fmt.Errorf("prompt not found: %s", req.Name)
}

// GetListChangedCapability advertises list-changed support when a change subscriber is configured.
func (pc *promptsCapability) GetListChangedCapability(ctx context.Context, session sessions.Session) (PromptListChangedCapability, bool, error) {
    if pc.staticContainer != nil {
        return promptsListChangedFromSubscriber{pc: pc}, true, nil
    }
    if pc.changeSub != nil {
        return promptsListChangedFromSubscriber{pc: pc}, true, nil
    }
    return nil, false, nil
}

// Helper: paginate static prompts
func (pc *promptsCapability) pagePrompts(all []mcp.Prompt, cursor *string) Page[mcp.Prompt] {
    start := parseCursor(cursor)
    if start < 0 || start > len(all) {
        start = 0
    }
    end := start + pc.pageSize
    if end > len(all) {
        end = len(all)
    }
    items := make([]mcp.Prompt, end-start)
    copy(items, all[start:end])
    if end < len(all) {
        next := strconv.Itoa(end)
        return NewPage(items, WithNextCursor[mcp.Prompt](next))
    }
    return NewPage(items)
}

// promptsListChangedFromSubscriber adapts a ChangeSubscriber to PromptListChangedCapability.
type promptsListChangedFromSubscriber struct{ pc *promptsCapability }

func (p promptsListChangedFromSubscriber) Register(ctx context.Context, session sessions.Session, fn NotifyPromptsListChangedFunc) (bool, error) {
    if p.pc == nil || fn == nil {
        return false, nil
    }
    var ch <-chan struct{}
    if p.pc.staticContainer != nil {
        ch = p.pc.staticContainer.Subscriber()
    } else if p.pc.changeSub != nil {
        ch = p.pc.changeSub.Subscriber()
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
