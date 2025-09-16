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
	ListResourcesFunc         func(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Resource], error)
	ListResourceTemplatesFunc func(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.ResourceTemplate], error)
	ReadResourceFunc          func(ctx context.Context, session sessions.Session, uri string) ([]mcp.ResourceContents, error)
)

// ResourcesOption is a functional option for configuring the resources capability.
type ResourcesOption func(*resourcesCapability)

type resourcesCapability struct {
	// Optional dynamic behavior
	listResourcesFn ListResourcesFunc
	listTemplatesFn ListResourceTemplatesFunc
	readResourceFn  ReadResourceFunc

	// Optional static resource container (enables subscribe capability)
	staticContainer *StaticResources

	// Static paging
	pageSize int

	// Optional change notification subscriber (process-local)
	changeSub ChangeSubscriber
}

// NewResourcesCapability constructs a ResourcesCapability using the provided options.
// It supports both static and dynamic modes via functional options.
func NewResourcesCapability(opts ...ResourcesOption) ResourcesCapability {
	rc := &resourcesCapability{
		pageSize: 50,
	}
	for _, opt := range opts {
		opt(rc)
	}
	return rc
}

// WithStaticResourceContainer enables static mode using the provided container.
// This will also allow the capability to expose subscription support.
func WithStaticResourceContainer(sr *StaticResources) ResourcesOption {
	return func(rc *resourcesCapability) {
		rc.staticContainer = sr
		// Auto-enable list-changed via the container's notifier.
		rc.changeSub = sr
	}
}

// WithListResources sets a custom list-resources function.
func WithListResources(fn ListResourcesFunc) ResourcesOption {
	return func(rc *resourcesCapability) { rc.listResourcesFn = fn }
}

// WithListResourceTemplates sets a custom list-templates function.
func WithListResourceTemplates(fn ListResourceTemplatesFunc) ResourcesOption {
	return func(rc *resourcesCapability) { rc.listTemplatesFn = fn }
}

// WithReadResource sets a custom read-resource function.
func WithReadResource(fn ReadResourceFunc) ResourcesOption {
	return func(rc *resourcesCapability) { rc.readResourceFn = fn }
}

// WithPageSize sets the page size for static pagination.
func WithPageSize(n int) ResourcesOption {
	return func(rc *resourcesCapability) {
		if n > 0 {
			rc.pageSize = n
		}
	}
}

// WithChangeNotification wires a ChangeSubscriber to enable list-changed notifications.
func WithChangeNotification(sub ChangeSubscriber) ResourcesOption {
	return func(rc *resourcesCapability) { rc.changeSub = sub }
}

// ListResources implements ResourcesCapability.
func (rc *resourcesCapability) ListResources(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Resource], error) {
	if rc.listResourcesFn != nil {
		return rc.listResourcesFn(ctx, session, cursor)
	}
	if rc.staticContainer != nil {
		return rc.pageResources(rc.staticContainer.SnapshotResources(), cursor), nil
	}
	return NewPage[mcp.Resource](nil), nil
}

// ListResourceTemplates implements ResourcesCapability.
func (rc *resourcesCapability) ListResourceTemplates(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.ResourceTemplate], error) {
	if rc.listTemplatesFn != nil {
		return rc.listTemplatesFn(ctx, session, cursor)
	}
	if rc.staticContainer != nil {
		return rc.pageTemplates(rc.staticContainer.SnapshotTemplates(), cursor), nil
	}
	return NewPage[mcp.ResourceTemplate](nil), nil
}

// ReadResource implements ResourcesCapability.
func (rc *resourcesCapability) ReadResource(ctx context.Context, session sessions.Session, uri string) ([]mcp.ResourceContents, error) {
	if rc.readResourceFn != nil {
		return rc.readResourceFn(ctx, session, uri)
	}
	if rc.staticContainer != nil {
		if c, ok := rc.staticContainer.ReadContents(uri); ok {
			return c, nil
		}
	}
	return nil, fmt.Errorf("resource not found: %s", uri)
}

// Helper: paginate static resources
func (rc *resourcesCapability) pageResources(all []mcp.Resource, cursor *string) Page[mcp.Resource] {
	start := parseCursor(cursor)
	if start < 0 || start > len(all) {
		start = 0
	}
	end := start + rc.pageSize
	if end > len(all) {
		end = len(all)
	}
	items := make([]mcp.Resource, end-start)
	copy(items, all[start:end])
	if end < len(all) {
		next := strconv.Itoa(end)
		return NewPage(items, WithNextCursor[mcp.Resource](next))
	}
	return NewPage(items)
}

// Helper: paginate static templates
func (rc *resourcesCapability) pageTemplates(all []mcp.ResourceTemplate, cursor *string) Page[mcp.ResourceTemplate] {
	start := parseCursor(cursor)
	if start < 0 || start > len(all) {
		start = 0
	}
	end := start + rc.pageSize
	if end > len(all) {
		end = len(all)
	}
	items := make([]mcp.ResourceTemplate, end-start)
	copy(items, all[start:end])
	if end < len(all) {
		next := strconv.Itoa(end)
		return NewPage(items, WithNextCursor[mcp.ResourceTemplate](next))
	}
	return NewPage(items)
}

func parseCursor(cursor *string) int {
	if cursor == nil {
		return 0
	}
	if *cursor == "" {
		return 0
	}
	n, err := strconv.Atoi(*cursor)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// GetSubscriptionCapability advertises per-URI subscription support when a static container is present.
func (rc *resourcesCapability) GetSubscriptionCapability(ctx context.Context, session sessions.Session) (ResourceSubscriptionCapability, bool, error) {
	if rc.staticContainer == nil {
		return nil, false, nil
	}
	return resourceSubscriptionFromContainer{sr: rc.staticContainer}, true, nil
}

// GetListChangedCapability advertises list-changed support when a change subscriber is configured.
func (rc *resourcesCapability) GetListChangedCapability(ctx context.Context, session sessions.Session) (ResourceListChangedCapability, bool, error) {
	if rc.changeSub == nil {
		return nil, false, nil
	}
	return resourceListChangedFromSubscriber{rc: rc}, true, nil
}

// resourceSubscriptionFromContainer adapts StaticResources to ResourceSubscriptionCapability.
type resourceSubscriptionFromContainer struct{ sr *StaticResources }

func (r resourceSubscriptionFromContainer) Subscribe(ctx context.Context, session sessions.Session, uri string) error {
	// Accept only URIs that exist
	if !r.sr.HasResource(uri) {
		return fmt.Errorf("resource not found: %s", uri)
	}
	sid := session.SessionID()
	_ = r.sr.Subscribe(ctx, sid, uri) // idempotent add
	return nil
}

// Unsubscribe removes a previously added subscription. It is idempotent.
func (r resourceSubscriptionFromContainer) Unsubscribe(ctx context.Context, session sessions.Session, uri string) error {
	if r.sr == nil {
		return nil
	}
	sid := session.SessionID()
	_ = r.sr.Unsubscribe(ctx, sid, uri) // idempotent remove
	return nil
}

// resourceListChangedFromSubscriber adapts a ChangeSubscriber to ResourceListChangedCapability.
type resourceListChangedFromSubscriber struct{ rc *resourcesCapability }

func (r resourceListChangedFromSubscriber) Register(ctx context.Context, session sessions.Session, fn NotifyResourceChangeFunc) (bool, error) {
	if r.rc == nil || r.rc.changeSub == nil || fn == nil {
		return false, nil
	}
	ch := r.rc.changeSub.Subscriber()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				// list_changed semantics: no specific URI
				fn(ctx, session, "")
			}
		}
	}()
	return true, nil
}
