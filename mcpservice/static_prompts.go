package mcpservice

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// PromptHandler handles a prompt get request to produce messages.
type PromptHandler func(ctx context.Context, session sessions.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error)

// StaticPrompt pairs a prompt descriptor with a handler that can materialize it.
type StaticPrompt struct {
	Descriptor mcp.Prompt
	Handler    PromptHandler
}

// PromptsContainer owns a mutable, threadsafe set of prompt descriptors and handlers.
// It allows simple servers to advertise a fixed (but updatable) set of prompts and
// have the server dispatch get requests automatically.
//
// PromptsContainer also embeds a ChangeNotifier and implements ChangeSubscriber to
// automatically expose listChanged support.
type PromptsContainer struct {
	mu       sync.RWMutex
	prompts  []mcp.Prompt
	handlers map[string]PromptHandler // name -> handler

	notifier ChangeNotifier
	pageSize int // pagination size (default 50)
}

// NewPromptsContainer constructs a new PromptsContainer with the given definitions.
func NewPromptsContainer(defs ...StaticPrompt) *PromptsContainer {
	sp := &PromptsContainer{pageSize: 50}
	// No parent context available here; Replace ignores ctx, so Background is fine.
	sp.Replace(context.Background(), defs...)
	return sp
}

// ProvidePrompts implements PromptsCapabilityProvider for the static container.
// Always reports present (ok=true).
func (sp *PromptsContainer) ProvidePrompts(ctx context.Context, session sessions.Session) (PromptsCapability, bool, error) {
	return sp, true, nil
}

// SetPageSize sets the pagination size for ListPrompts.
func (sp *PromptsContainer) SetPageSize(n int) {
	if n > 0 {
		sp.mu.Lock()
		sp.pageSize = n
		sp.mu.Unlock()
	}
}

// Snapshot returns a copy of the current prompt descriptors.
func (sp *PromptsContainer) Snapshot() []mcp.Prompt {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	out := make([]mcp.Prompt, len(sp.prompts))
	copy(out, sp.prompts)
	return out
}

// Replace atomically replaces the entire prompt set.
func (sp *PromptsContainer) Replace(_ context.Context, defs ...StaticPrompt) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.prompts = sp.prompts[:0]
	if cap(sp.prompts) < len(defs) {
		sp.prompts = make([]mcp.Prompt, 0, len(defs))
	}
	sp.handlers = make(map[string]PromptHandler, len(defs))
	for _, d := range defs {
		sp.prompts = append(sp.prompts, d.Descriptor)
		if d.Handler != nil {
			sp.handlers[d.Descriptor.Name] = d.Handler
		}
	}
	// notify listeners of change (best-effort)
	go func() { _ = sp.notifier.Notify(context.Background()) }()
}

// Add registers a new prompt if it doesn't duplicate an existing name.
// Returns true if added.
func (sp *PromptsContainer) Add(_ context.Context, def StaticPrompt) bool {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.handlers == nil {
		sp.handlers = make(map[string]PromptHandler)
	}
	name := def.Descriptor.Name
	if name == "" {
		return false
	}
	if _, exists := sp.handlers[name]; exists {
		return false
	}
	for _, p := range sp.prompts {
		if p.Name == name {
			return false
		}
	}
	sp.prompts = append(sp.prompts, def.Descriptor)
	if def.Handler != nil {
		sp.handlers[name] = def.Handler
	}
	go func() { _ = sp.notifier.Notify(context.Background()) }()
	return true
}

// Remove removes a prompt by name. Returns true if removed.
func (sp *PromptsContainer) Remove(_ context.Context, name string) bool {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	n := 0
	removed := false
	for _, p := range sp.prompts {
		if p.Name == name {
			removed = true
			continue
		}
		sp.prompts[n] = p
		n++
	}
	sp.prompts = sp.prompts[:n]
	if removed {
		delete(sp.handlers, name)
		go func() { _ = sp.notifier.Notify(context.Background()) }()
	}
	return removed
}

// Get dispatches a prompt get to the named handler if present.
func (sp *PromptsContainer) Get(ctx context.Context, session sessions.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error) {
	if req == nil || req.Name == "" {
		return nil, fmt.Errorf("invalid prompt request: missing name")
	}
	sp.mu.RLock()
	h := sp.handlers[req.Name]
	sp.mu.RUnlock()
	if h == nil {
		return nil, fmt.Errorf("prompt not found: %s", req.Name)
	}
	return h(ctx, session, req)
}

// Subscriber implements ChangeSubscriber by returning a per-subscriber channel
// that receives a signal whenever the prompt set changes.
func (sp *PromptsContainer) Subscriber() <-chan struct{} { return sp.notifier.Subscriber() }

// JSON helper for building prompt messages easily
func JSONMessages(msgs []mcp.PromptMessage) json.RawMessage {
	b, _ := json.Marshal(msgs)
	return b
}
