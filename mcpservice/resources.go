package mcpservice

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// --- Dynamic resources implementation ---

type (
	ListResourcesFunc         func(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Resource], error)
	ListResourceTemplatesFunc func(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.ResourceTemplate], error)
	ReadResourceFunc          func(ctx context.Context, session sessions.Session, uri string) ([]mcp.ResourceContents, error)
)

type DynamicResourcesOption func(*dynamicResources)

type dynamicResources struct {
	listFn    ListResourcesFunc
	listTplFn ListResourceTemplatesFunc
	readFn    ReadResourceFunc
	subCap    ResourceSubscriptionCapability
	changeSub ChangeSubscriber
}

func NewDynamicResources(opts ...DynamicResourcesOption) ResourcesCapability {
	dr := &dynamicResources{}
	for _, opt := range opts {
		opt(dr)
	}
	return dr
}

func WithResourcesListFunc(fn ListResourcesFunc) DynamicResourcesOption {
	return func(d *dynamicResources) { d.listFn = fn }
}
func WithResourcesListTemplatesFunc(fn ListResourceTemplatesFunc) DynamicResourcesOption {
	return func(d *dynamicResources) { d.listTplFn = fn }
}
func WithResourcesReadFunc(fn ReadResourceFunc) DynamicResourcesOption {
	return func(d *dynamicResources) { d.readFn = fn }
}
func WithResourcesSubscriptionCapability(cap ResourceSubscriptionCapability) DynamicResourcesOption {
	return func(d *dynamicResources) { d.subCap = cap }
}
func WithResourcesChangeSubscriber(sub ChangeSubscriber) DynamicResourcesOption {
	return func(d *dynamicResources) { d.changeSub = sub }
}

func (d *dynamicResources) ListResources(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Resource], error) {
	if d.listFn == nil {
		return NewPage[mcp.Resource](nil), nil
	}
	return d.listFn(ctx, session, cursor)
}
func (d *dynamicResources) ListResourceTemplates(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.ResourceTemplate], error) {
	if d.listTplFn == nil {
		return NewPage[mcp.ResourceTemplate](nil), nil
	}
	return d.listTplFn(ctx, session, cursor)
}
func (d *dynamicResources) ReadResource(ctx context.Context, session sessions.Session, uri string) ([]mcp.ResourceContents, error) {
	if d.readFn == nil {
		return nil, fmt.Errorf("resource not found: %s", uri)
	}
	return d.readFn(ctx, session, uri)
}
func (d *dynamicResources) GetSubscriptionCapability(ctx context.Context, session sessions.Session) (ResourceSubscriptionCapability, bool, error) {
	if d.subCap == nil {
		return nil, false, nil
	}
	return d.subCap, true, nil
}
func (d *dynamicResources) GetListChangedCapability(ctx context.Context, session sessions.Session) (ResourceListChangedCapability, bool, error) {
	if d.changeSub == nil {
		return nil, false, nil
	}
	return resourceListChangedFromSubscriber{sub: d.changeSub}, true, nil
}

// parseCursor shared helper
func parseCursor(cursor *string) int {
	if cursor == nil || *cursor == "" {
		return 0
	}
	n, err := strconv.Atoi(*cursor)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// resourceListChangedFromSubscriber adapts ChangeSubscriber to ResourceListChangedCapability.
type resourceListChangedFromSubscriber struct{ sub ChangeSubscriber }

func (r resourceListChangedFromSubscriber) Register(ctx context.Context, session sessions.Session, fn NotifyResourceChangeFunc) (bool, error) {
	if r.sub == nil || fn == nil {
		return false, nil
	}
	ch := r.sub.Subscriber()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				fn(ctx, session, "")
			}
		}
	}()
	return true, nil
}
