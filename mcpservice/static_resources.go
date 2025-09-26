package mcpservice

import (
	"context"
	"fmt"
	"sync"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// ResourcesContainer owns a mutable, threadsafe set of resources, templates,
// and their contents. It also tracks per-session subscriptions and will prune
// subscriptions automatically when resources are removed.
type ResourcesContainer struct {
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

	// updatedNotifiers contains per-URI notifiers that tick when contents for a
	// URI are updated. This is used for bridging notifications/resources/updated
	// in transports that support subscriptions.
	updatedNotifiers map[string]*ChangeNotifier

	pageSize int // pagination size (default 50)
}

// SetPageSize configures the maximum number of items returned per page when
// listing resources or templates. Values < 1 are ignored.
func (sr *ResourcesContainer) SetPageSize(n int) {
	if n < 1 {
		return
	}
	sr.mu.Lock()
	sr.pageSize = n
	sr.mu.Unlock()
}

// NewResourcesContainer constructs a ResourcesContainer with initial
// resources, templates and contents. Slices and maps are copied so callers may
// retain ownership of their inputs.
func NewResourcesContainer(resources []mcp.Resource, templates []mcp.ResourceTemplate, contents map[string][]mcp.ResourceContents) *ResourcesContainer {
	sr := &ResourcesContainer{
		contents:         make(map[string][]mcp.ResourceContents),
		uriSet:           make(map[string]struct{}),
		subsByURI:        make(map[string]map[string]struct{}),
		subsBySession:    make(map[string]map[string]struct{}),
		updatedNotifiers: make(map[string]*ChangeNotifier),
		pageSize:         50,
	}
	sr.ReplaceResources(context.Background(), resources)
	sr.ReplaceTemplates(context.Background(), templates)
	sr.ReplaceAllContents(context.Background(), contents)
	return sr
}

// ProvideResources implements ResourcesCapabilityProvider for a static container.
// Always returns itself as present (ok=true) even if empty.
func (sr *ResourcesContainer) ProvideResources(ctx context.Context, session sessions.Session) (ResourcesCapability, bool, error) {
	return sr, true, nil
}

// SnapshotResources returns a copy of the current resources slice.
func (sr *ResourcesContainer) SnapshotResources() []mcp.Resource {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	out := make([]mcp.Resource, len(sr.resources))
	copy(out, sr.resources)
	return out
}

// SnapshotTemplates returns a copy of the current templates slice.
func (sr *ResourcesContainer) SnapshotTemplates() []mcp.ResourceTemplate {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	out := make([]mcp.ResourceTemplate, len(sr.templates))
	copy(out, sr.templates)
	return out
}

// ReadContents returns a copy of the contents for a URI if present.
func (sr *ResourcesContainer) ReadContents(uri string) ([]mcp.ResourceContents, bool) {
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
func (sr *ResourcesContainer) HasResource(uri string) bool {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	_, ok := sr.uriSet[uri]
	return ok
}

// ReplaceResources atomically replaces the static resource set.
// It returns the list of URIs that were removed, and prunes any subscriptions to them.
func (sr *ResourcesContainer) ReplaceResources(_ context.Context, resources []mcp.Resource) (removedURIs []string) {
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
func (sr *ResourcesContainer) ReplaceTemplates(_ context.Context, templates []mcp.ResourceTemplate) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.templates = make([]mcp.ResourceTemplate, len(templates))
	copy(sr.templates, templates)
	// Signal list-changed (best effort)
	go func() { _ = sr.notifier.Notify(context.Background()) }()
}

// ReplaceAllContents atomically replaces contents.
func (sr *ResourcesContainer) ReplaceAllContents(_ context.Context, contents map[string][]mcp.ResourceContents) {
	sr.mu.Lock()
	// Track which URIs were updated so we can signal after releasing the lock.
	var updatedURIs []string
	if contents == nil {
		sr.contents = make(map[string][]mcp.ResourceContents)
		// Nothing to signal
		sr.mu.Unlock()
		return
	}
	sr.contents = make(map[string][]mcp.ResourceContents, len(contents))
	for k, v := range contents {
		vv := make([]mcp.ResourceContents, len(v))
		copy(vv, v)
		sr.contents[k] = vv
		updatedURIs = append(updatedURIs, k)
	}
	sr.mu.Unlock()
	// Best-effort: notify per-URI updated after contents replaced.
	for _, uri := range updatedURIs {
		sr.markUpdated(uri)
	}
}

// AddResource adds a resource; returns true if newly added.
func (sr *ResourcesContainer) AddResource(_ context.Context, res mcp.Resource) bool {
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
func (sr *ResourcesContainer) RemoveResource(_ context.Context, uri string) bool {
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
func (sr *ResourcesContainer) Subscribe(_ context.Context, sessionID string, uri string) bool {
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
func (sr *ResourcesContainer) Unsubscribe(_ context.Context, sessionID string, uri string) bool {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return sr.unsubscribeLocked(sessionID, uri)
}

func (sr *ResourcesContainer) unsubscribeLocked(sessionID string, uri string) bool {
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
func (sr *ResourcesContainer) UnsubscribeAllForSession(_ context.Context, sessionID string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	uris := sr.listURIsForSessionLocked(sessionID)
	for _, uri := range uris {
		sr.unsubscribeLocked(sessionID, uri)
	}
}

// ListSessionsForURI returns the set of sessionIDs subscribed to a URI.
func (sr *ResourcesContainer) ListSessionsForURI(_ context.Context, uri string) []string {
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
func (sr *ResourcesContainer) Subscriber() <-chan struct{} {
	return sr.notifier.Subscriber()
}

// SubscriberForURI returns a channel that receives a tick each time the
// specific URI's contents are updated via ReplaceAllContents (or other future
// content mutation paths). The channel is closed when the notifier is GC'd.
func (sr *ResourcesContainer) SubscriberForURI(uri string) <-chan struct{} {
	sr.mu.Lock()
	if sr.updatedNotifiers == nil {
		sr.updatedNotifiers = make(map[string]*ChangeNotifier)
	}
	n := sr.updatedNotifiers[uri]
	if n == nil {
		n = &ChangeNotifier{}
		sr.updatedNotifiers[uri] = n
	}
	sr.mu.Unlock()
	return n.Subscriber()
}

// markUpdated triggers a best-effort tick on the per-URI notifier, if any.
func (sr *ResourcesContainer) markUpdated(uri string) {
	sr.mu.RLock()
	n := sr.updatedNotifiers[uri]
	sr.mu.RUnlock()
	if n == nil {
		// Lazily create to ensure future subscribers see notifications.
		sr.mu.Lock()
		if sr.updatedNotifiers == nil {
			sr.updatedNotifiers = make(map[string]*ChangeNotifier)
		}
		if sr.updatedNotifiers[uri] == nil {
			sr.updatedNotifiers[uri] = &ChangeNotifier{}
		}
		n = sr.updatedNotifiers[uri]
		sr.mu.Unlock()
	}
	// Best-effort notify without holding locks.
	_ = n.Notify(context.Background())
}

func (sr *ResourcesContainer) listURIsForSessionLocked(sessionID string) []string {
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

func (sr *ResourcesContainer) unsubscribeAllForURILocked(uri string) {
	if sessions := sr.subsByURI[uri]; len(sessions) > 0 {
		for sid := range sessions {
			sr.unsubscribeLocked(sid, uri)
		}
	}
}

// --- ResourcesCapability implementation (static mode) ---

// ListResources implements ResourcesCapability.
func (sr *ResourcesContainer) ListResources(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Resource], error) {
	sr.mu.RLock()
	all := make([]mcp.Resource, len(sr.resources))
	copy(all, sr.resources)
	pageSize := sr.pageSize
	sr.mu.RUnlock()
	start := parseCursor(cursor)
	if start < 0 || start > len(all) {
		start = 0
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	items := make([]mcp.Resource, end-start)
	copy(items, all[start:end])
	if end < len(all) {
		return NewPage(items, WithNextCursor[mcp.Resource](fmt.Sprintf("%d", end))), nil
	}
	return NewPage(items), nil
}

// ListResourceTemplates implements ResourcesCapability.
func (sr *ResourcesContainer) ListResourceTemplates(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.ResourceTemplate], error) {
	sr.mu.RLock()
	all := make([]mcp.ResourceTemplate, len(sr.templates))
	copy(all, sr.templates)
	pageSize := sr.pageSize
	sr.mu.RUnlock()
	start := parseCursor(cursor)
	if start < 0 || start > len(all) {
		start = 0
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	items := make([]mcp.ResourceTemplate, end-start)
	copy(items, all[start:end])
	if end < len(all) {
		return NewPage(items, WithNextCursor[mcp.ResourceTemplate](fmt.Sprintf("%d", end))), nil
	}
	return NewPage(items), nil
}

// ReadResource implements ResourcesCapability.
func (sr *ResourcesContainer) ReadResource(ctx context.Context, session sessions.Session, uri string) ([]mcp.ResourceContents, error) {
	if c, ok := sr.ReadContents(uri); ok {
		return c, nil
	}
	return nil, fmt.Errorf("resource not found: %s", uri)
}

// GetSubscriptionCapability implements ResourcesCapability.
func (sr *ResourcesContainer) GetSubscriptionCapability(ctx context.Context, session sessions.Session) (ResourceSubscriptionCapability, bool, error) {
	return resourceSubscriptionFromContainer{sr: sr}, true, nil
}

// GetListChangedCapability implements ResourcesCapability.
func (sr *ResourcesContainer) GetListChangedCapability(ctx context.Context, session sessions.Session) (ResourceListChangedCapability, bool, error) {
	return resourceListChangedFromSubscriber{sub: sr}, true, nil
}

// resourceSubscriptionFromContainer adapts ResourcesContainer to ResourceSubscriptionCapability.
type resourceSubscriptionFromContainer struct{ sr *ResourcesContainer }

func (r resourceSubscriptionFromContainer) Subscribe(ctx context.Context, session sessions.Session, uri string, emit NotifyResourceUpdatedFunc) (CancelSubscription, error) {
	if r.sr == nil {
		return func(context.Context) error { return nil }, nil
	}
	if !r.sr.HasResource(uri) {
		return nil, fmt.Errorf("resource not found: %s", uri)
	}
	sid := session.SessionID()
	_ = r.sr.Subscribe(ctx, sid, uri)
	fwdCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	ch := r.sr.SubscriberForURI(uri)
	go func() {
		for {
			select {
			case <-fwdCtx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				select {
				case <-fwdCtx.Done():
					return
				default:
				}
				if emit != nil {
					emit(context.WithoutCancel(fwdCtx), uri)
				}
			}
		}
	}()
	cancel := func(ctx context.Context) error { stop(); _ = r.sr.Unsubscribe(ctx, sid, uri); return nil }
	return cancel, nil
}
