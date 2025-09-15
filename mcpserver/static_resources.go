package mcpserver

import (
	"context"
	"sync"

	"github.com/ggoodman/mcp-server-go/mcp"
)

// StaticResources owns a mutable, threadsafe set of static resources, templates,
// and their contents. It also tracks per-session subscriptions and will prune
// subscriptions automatically when resources are removed.
type StaticResources struct {
	mu sync.RWMutex

	resources []mcp.Resource
	templates []mcp.ResourceTemplate
	contents  map[string][]mcp.ResourceContents

	// URI membership index for quick existence checks and diffing
	uriSet map[string]struct{}

	// Subscription registry
	subsByURI     map[string]map[string]struct{} // uri -> set(sessionID)
	subsBySession map[string]map[string]struct{} // sessionID -> set(uri)

	// notifier emits a signal when the resource list (or templates) changes.
	notifier ChangeNotifier
}

// NewStaticResources constructs a StaticResources container with initial
// resources, templates and contents. Slices and maps are copied so callers may
// retain ownership of their inputs.
func NewStaticResources(resources []mcp.Resource, templates []mcp.ResourceTemplate, contents map[string][]mcp.ResourceContents) *StaticResources {
	sr := &StaticResources{
		contents:      make(map[string][]mcp.ResourceContents),
		uriSet:        make(map[string]struct{}),
		subsByURI:     make(map[string]map[string]struct{}),
		subsBySession: make(map[string]map[string]struct{}),
	}
	sr.ReplaceResources(context.Background(), resources)
	sr.ReplaceTemplates(context.Background(), templates)
	sr.ReplaceAllContents(context.Background(), contents)
	return sr
}

// SnapshotResources returns a copy of the current resources slice.
func (sr *StaticResources) SnapshotResources() []mcp.Resource {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	out := make([]mcp.Resource, len(sr.resources))
	copy(out, sr.resources)
	return out
}

// SnapshotTemplates returns a copy of the current templates slice.
func (sr *StaticResources) SnapshotTemplates() []mcp.ResourceTemplate {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	out := make([]mcp.ResourceTemplate, len(sr.templates))
	copy(out, sr.templates)
	return out
}

// ReadContents returns a copy of the contents for a URI if present.
func (sr *StaticResources) ReadContents(uri string) ([]mcp.ResourceContents, bool) {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	c, ok := sr.contents[uri]
	if !ok {
		return nil, false
	}
	out := make([]mcp.ResourceContents, len(c))
	copy(out, c)
	return out, true
}

// HasResource reports whether a URI exists in the current static set.
func (sr *StaticResources) HasResource(uri string) bool {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	_, ok := sr.uriSet[uri]
	return ok
}

// ReplaceResources atomically replaces the static resource set.
// It returns the list of URIs that were removed, and prunes any subscriptions to them.
func (sr *StaticResources) ReplaceResources(_ context.Context, resources []mcp.Resource) (removedURIs []string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	// Build new uri set
	newURIs := make(map[string]struct{}, len(resources))
	for _, r := range resources {
		newURIs[r.URI] = struct{}{}
	}

	// Compute removed URIs
	for uri := range sr.uriSet {
		if _, ok := newURIs[uri]; !ok {
			removedURIs = append(removedURIs, uri)
		}
	}

	// Replace data
	sr.resources = make([]mcp.Resource, len(resources))
	copy(sr.resources, resources)
	sr.uriSet = newURIs

	// Prune subscriptions to removed URIs
	for _, uri := range removedURIs {
		sr.unsubscribeAllForURILocked(uri)
	}
	// Signal list-changed (best effort)
	go func() { _ = sr.notifier.Notify(context.Background()) }()
	return removedURIs
}

// ReplaceTemplates atomically replaces templates.
func (sr *StaticResources) ReplaceTemplates(_ context.Context, templates []mcp.ResourceTemplate) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.templates = make([]mcp.ResourceTemplate, len(templates))
	copy(sr.templates, templates)
	// Signal list-changed (best effort)
	go func() { _ = sr.notifier.Notify(context.Background()) }()
}

// ReplaceAllContents atomically replaces contents.
func (sr *StaticResources) ReplaceAllContents(_ context.Context, contents map[string][]mcp.ResourceContents) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if contents == nil {
		sr.contents = make(map[string][]mcp.ResourceContents)
		return
	}
	sr.contents = make(map[string][]mcp.ResourceContents, len(contents))
	for k, v := range contents {
		vv := make([]mcp.ResourceContents, len(v))
		copy(vv, v)
		sr.contents[k] = vv
	}
}

// AddResource adds a resource; returns true if newly added.
func (sr *StaticResources) AddResource(_ context.Context, res mcp.Resource) bool {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if _, exists := sr.uriSet[res.URI]; exists {
		return false
	}
	sr.resources = append(sr.resources, res)
	if sr.uriSet == nil {
		sr.uriSet = make(map[string]struct{})
	}
	sr.uriSet[res.URI] = struct{}{}
	// Signal list-changed (best effort)
	go func() { _ = sr.notifier.Notify(context.Background()) }()
	return true
}

// RemoveResource removes a resource by URI; returns true if removed.
// Any subscriptions for this URI are pruned.
func (sr *StaticResources) RemoveResource(_ context.Context, uri string) bool {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if _, exists := sr.uriSet[uri]; !exists {
		return false
	}
	// remove from slice (order not preserved strictly; stable remove if needed later)
	n := 0
	for _, r := range sr.resources {
		if r.URI != uri {
			sr.resources[n] = r
			n++
		}
	}
	sr.resources = sr.resources[:n]
	delete(sr.uriSet, uri)
	sr.unsubscribeAllForURILocked(uri)
	// Signal list-changed (best effort)
	go func() { _ = sr.notifier.Notify(context.Background()) }()
	return true
}

// Subscribe adds a subscription mapping; returns true if newly added.
func (sr *StaticResources) Subscribe(_ context.Context, sessionID string, uri string) bool {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if _, ok := sr.uriSet[uri]; !ok {
		return false
	}
	if _, ok := sr.subsByURI[uri]; !ok {
		sr.subsByURI[uri] = make(map[string]struct{})
	}
	if _, ok := sr.subsBySession[sessionID]; !ok {
		sr.subsBySession[sessionID] = make(map[string]struct{})
	}
	_, existed := sr.subsByURI[uri][sessionID]
	sr.subsByURI[uri][sessionID] = struct{}{}
	sr.subsBySession[sessionID][uri] = struct{}{}
	return !existed
}

// Unsubscribe removes a subscription mapping; returns true if removed.
func (sr *StaticResources) Unsubscribe(_ context.Context, sessionID string, uri string) bool {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return sr.unsubscribeLocked(sessionID, uri)
}

func (sr *StaticResources) unsubscribeLocked(sessionID string, uri string) bool {
	changed := false
	if m, ok := sr.subsByURI[uri]; ok {
		if _, ok := m[sessionID]; ok {
			delete(m, sessionID)
			changed = true
			if len(m) == 0 {
				delete(sr.subsByURI, uri)
			}
		}
	}
	if m, ok := sr.subsBySession[sessionID]; ok {
		if _, ok := m[uri]; ok {
			delete(m, uri)
			if len(m) == 0 {
				delete(sr.subsBySession, sessionID)
			}
		}
	}
	return changed
}

// UnsubscribeAllForSession removes all subscriptions for a sessionID.
func (sr *StaticResources) UnsubscribeAllForSession(_ context.Context, sessionID string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	uris := sr.listURIsForSessionLocked(sessionID)
	for _, uri := range uris {
		sr.unsubscribeLocked(sessionID, uri)
	}
}

// ListSessionsForURI returns the set of sessionIDs subscribed to a URI.
func (sr *StaticResources) ListSessionsForURI(_ context.Context, uri string) []string {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	sessions := sr.subsByURI[uri]
	if len(sessions) == 0 {
		return nil
	}
	out := make([]string, 0, len(sessions))
	for sid := range sessions {
		out = append(out, sid)
	}
	return out
}

// Subscriber implements ChangeSubscriber by returning a channel that
// receives a signal whenever the resource list or templates change.
func (sr *StaticResources) Subscriber() <-chan struct{} {
	return sr.notifier.Subscriber()
}

func (sr *StaticResources) listURIsForSessionLocked(sessionID string) []string {
	m := sr.subsBySession[sessionID]
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for uri := range m {
		out = append(out, uri)
	}
	return out
}

func (sr *StaticResources) unsubscribeAllForURILocked(uri string) {
	if sessions := sr.subsByURI[uri]; len(sessions) > 0 {
		for sid := range sessions {
			sr.unsubscribeLocked(sid, uri)
		}
	}
}
