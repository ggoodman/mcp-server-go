package mcpservice

// Rewritten static resources container providing an ergonomic API similar to
// ToolsContainer. This version intentionally decouples the public API from the
// wire-level MCP resource content types while still implementing
// ResourcesCapability internally for the engine/handler.

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"sync"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// Resource models a static resource's metadata and its content variants. Each
// element in Variants is mapped to a distinct mcp.ResourceContents when read.
// A resource may have zero variants (allowing metadata-only listing) and can
// later be Upserted with content.
type Resource struct {
	URI      string
	Name     string
	Desc     string
	MimeType string
	Variants []ContentVariant
}

// ContentVariant represents one textual or binary variant of a resource. Text
// and Blob are mutually exclusive; if both are populated Text takes precedence.
// MimeType overrides the parent Resource.MimeType when non-empty.
type ContentVariant struct {
	Text     string
	Blob     []byte
	MimeType string
}

// ResourceTemplate mirrors the MCP resource template shape while remaining
// decoupled from the wire types.
type ResourceTemplate struct {
	URITemplate string
	Name        string
	Desc        string
	MimeType    string
}

// Helper constructors ----------------------------------------------------------------

func TextResource(uri, text string, opts ...ResourceOption) Resource {
	r := Resource{URI: uri, Variants: []ContentVariant{TextVariant(text)}}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

func BlobResource(uri string, blob []byte, mime string, opts ...ResourceOption) Resource {
	r := Resource{URI: uri, MimeType: mime, Variants: []ContentVariant{BlobVariant(blob, mime)}}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

func ResourceWithVariants(uri string, variants []ContentVariant, opts ...ResourceOption) Resource {
	cp := make([]ContentVariant, len(variants))
	copy(cp, variants)
	r := Resource{URI: uri, Variants: cp}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

func NewTemplate(uriTemplate string, opts ...TemplateOption) ResourceTemplate {
	t := ResourceTemplate{URITemplate: uriTemplate}
	for _, opt := range opts {
		opt(&t)
	}
	return t
}

// Variant helpers --------------------------------------------------------------------

func TextVariant(text string, mime ...string) ContentVariant {
	cv := ContentVariant{Text: text}
	if len(mime) > 0 {
		cv.MimeType = mime[0]
	}
	return cv
}

func BlobVariant(data []byte, mime string) ContentVariant {
	cp := make([]byte, len(data))
	copy(cp, data)
	return ContentVariant{Blob: cp, MimeType: mime}
}

// Options ---------------------------------------------------------------------------

type ResourceOption func(*Resource)

func WithName(name string) ResourceOption        { return func(r *Resource) { r.Name = name } }
func WithDescription(desc string) ResourceOption { return func(r *Resource) { r.Desc = desc } }
func WithMimeType(mime string) ResourceOption    { return func(r *Resource) { r.MimeType = mime } }
func WithVariants(vs ...ContentVariant) ResourceOption {
	return func(r *Resource) {
		if len(vs) == 0 {
			return
		}
		r.Variants = append(r.Variants, vs...)
	}
}

type TemplateOption func(*ResourceTemplate)

func WithTemplateName(name string) TemplateOption { return func(t *ResourceTemplate) { t.Name = name } }
func WithTemplateDescription(desc string) TemplateOption {
	return func(t *ResourceTemplate) { t.Desc = desc }
}
func WithTemplateMimeType(mime string) TemplateOption {
	return func(t *ResourceTemplate) { t.MimeType = mime }
}

// Container options -----------------------------------------------------------------

type ContainerOption func(*resourcesContainerConfig)

type resourcesContainerConfig struct {
	pageSize int
}

// WithPageSize sets initial pagination size. Values <1 ignored (default 50).
func WithPageSize(n int) ContainerOption {
	return func(c *resourcesContainerConfig) {
		if n > 0 {
			c.pageSize = n
		}
	}
}

// ResourcesContainer owns a static, in-memory collection of resources and
// templates with subscription support and change notifications. CRUD methods
// are synchronous and do not take contexts; capability methods (List*/Read*)
// adapt to context-aware interfaces required by the engine.
type ResourcesContainer struct {
	mu sync.RWMutex

	resources []Resource
	templates []ResourceTemplate

	// index for fast existence checks
	uriSet map[string]int // uri -> index in resources slice

	// contents per URI (derived from resources.Variants). Stored pre-translated
	// to avoid repeated base64 work? We intentionally store logical variants
	// and translate on Read to keep API simpler and avoid premature caching.

	// subscriptions
	subsByURI     map[string]map[string]struct{}
	subsBySession map[string]map[string]struct{}

	notifier         ChangeNotifier
	updatedNotifiers map[string]*ChangeNotifier

	pageSize int
}

// NewResourcesContainer constructs an empty container.
func NewResourcesContainer(opts ...ContainerOption) *ResourcesContainer {
	cfg := resourcesContainerConfig{pageSize: 50}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &ResourcesContainer{
		uriSet:           make(map[string]int),
		subsByURI:        make(map[string]map[string]struct{}),
		subsBySession:    make(map[string]map[string]struct{}),
		updatedNotifiers: make(map[string]*ChangeNotifier),
		pageSize:         cfg.pageSize,
	}
}

// SetPageSize adjusts pagination size (ignored if n<1).
func (rc *ResourcesContainer) SetPageSize(n int) {
	if n > 0 {
		rc.mu.Lock()
		rc.pageSize = n
		rc.mu.Unlock()
	}
}

// HasResource reports whether a resource URI exists.
func (rc *ResourcesContainer) HasResource(uri string) bool {
	rc.mu.RLock()
	_, ok := rc.uriSet[uri]
	rc.mu.RUnlock()
	return ok
}

// SnapshotResources returns a copy of current resources.
func (rc *ResourcesContainer) SnapshotResources() []Resource {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	out := make([]Resource, len(rc.resources))
	copy(out, rc.resources)
	return out
}

// SnapshotTemplates returns a copy of current templates.
func (rc *ResourcesContainer) SnapshotTemplates() []ResourceTemplate {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	out := make([]ResourceTemplate, len(rc.templates))
	copy(out, rc.templates)
	return out
}

// Read returns a defensive copy of the resource's content variants.
func (rc *ResourcesContainer) Read(uri string) ([]ContentVariant, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	idx, ok := rc.uriSet[uri]
	if !ok {
		return nil, false
	}
	res := rc.resources[idx]
	out := make([]ContentVariant, len(res.Variants))
	copy(out, res.Variants)
	return out, true
}

// AddResource adds a new resource; returns true if newly added.
func (rc *ResourcesContainer) AddResource(r Resource) bool {
	if r.URI == "" {
		return false
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if _, exists := rc.uriSet[r.URI]; exists {
		return false
	}
	rc.resources = append(rc.resources, normalizeResource(r))
	rc.uriSet[r.URI] = len(rc.resources) - 1
	go func() { _ = rc.notifier.Notify(context.Background()) }()
	return true
}

// UpsertResource inserts or updates a resource. Returns true if created.
// Emits listChanged if created or metadata (Name/Desc/MimeType) changed.
// Emits per-URI updated only if variants changed.
func (rc *ResourcesContainer) UpsertResource(r Resource) (created bool) {
	if r.URI == "" {
		return false
	}
	nr := normalizeResource(r)
	rc.mu.Lock()
	if idx, ok := rc.uriSet[nr.URI]; ok {
		prev := rc.resources[idx]
		metaChanged := prev.Name != nr.Name || prev.Desc != nr.Desc || prev.MimeType != nr.MimeType
		variantsChanged := !equalVariants(prev.Variants, nr.Variants)
		rc.resources[idx] = nr
		rc.mu.Unlock()
		if metaChanged {
			go func() { _ = rc.notifier.Notify(context.Background()) }()
		}
		if variantsChanged {
			rc.markUpdated(nr.URI)
		}
		return false
	}
	rc.resources = append(rc.resources, nr)
	rc.uriSet[nr.URI] = len(rc.resources) - 1
	rc.mu.Unlock()
	go func() { _ = rc.notifier.Notify(context.Background()) }()
	if len(nr.Variants) > 0 {
		rc.markUpdated(nr.URI)
	}
	return true
}

// RemoveResource removes a resource by URI.
func (rc *ResourcesContainer) RemoveResource(uri string) bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	idx, ok := rc.uriSet[uri]
	if !ok {
		return false
	}
	// remove without preserving order (swap delete)
	last := len(rc.resources) - 1
	rc.resources[idx] = rc.resources[last]
	rc.uriSet[rc.resources[idx].URI] = idx
	rc.resources = rc.resources[:last]
	delete(rc.uriSet, uri)
	go func() { _ = rc.notifier.Notify(context.Background()) }()
	return true
}

// ReplaceResources atomically replaces the entire resource set. Returns URIs removed.
func (rc *ResourcesContainer) ReplaceResources(resources []Resource) (removed []string) {
	nr := make([]Resource, 0, len(resources))
	uriSet := make(map[string]int, len(resources))
	for _, r := range resources {
		if r.URI != "" {
			nr = append(nr, normalizeResource(r))
		}
	}
	// Build new index
	for i, r := range nr {
		uriSet[r.URI] = i
	}

	rc.mu.Lock()
	// Compute removed
	for uri := range rc.uriSet {
		if _, ok := uriSet[uri]; !ok {
			removed = append(removed, uri)
		}
	}
	rc.resources = nr
	rc.uriSet = uriSet
	// clear per-URI notifiers for removed URIs
	for _, uri := range removed {
		delete(rc.updatedNotifiers, uri)
	}
	rc.mu.Unlock()
	// Always signal listChanged even if no structural diff so callers can
	// force a refresh (parity with tools container + test expectations).
	go func() { _ = rc.notifier.Notify(context.Background()) }()
	// mark all updated (conservative) – could diff but simplicity wins
	for _, r := range nr {
		if len(r.Variants) > 0 {
			rc.markUpdated(r.URI)
		}
	}
	return removed
}

// Template CRUD ---------------------------------------------------------------------

// AddTemplate adds a template; returns true if new.
func (rc *ResourcesContainer) AddTemplate(t ResourceTemplate) bool {
	if t.URITemplate == "" {
		return false
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for _, existing := range rc.templates {
		if existing.URITemplate == t.URITemplate {
			return false
		}
	}
	rc.templates = append(rc.templates, t)
	go func() { _ = rc.notifier.Notify(context.Background()) }()
	return true
}

// RemoveTemplate removes a template by uriTemplate.
func (rc *ResourcesContainer) RemoveTemplate(uriTemplate string) bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	n := 0
	removed := false
	for _, t := range rc.templates {
		if t.URITemplate == uriTemplate {
			removed = true
			continue
		}
		rc.templates[n] = t
		n++
	}
	if !removed {
		return false
	}
	rc.templates = rc.templates[:n]
	go func() { _ = rc.notifier.Notify(context.Background()) }()
	return true
}

// ReplaceTemplates atomically replaces templates.
func (rc *ResourcesContainer) ReplaceTemplates(ts []ResourceTemplate) {
	cp := make([]ResourceTemplate, len(ts))
	copy(cp, ts)
	rc.mu.Lock()
	rc.templates = cp
	rc.mu.Unlock()
	go func() { _ = rc.notifier.Notify(context.Background()) }()
}

// Event source accessors (engine-owned subscription lifecycle) ----------------------

// ListChangedChan returns a channel signaled when resources/templates set changes.
func (rc *ResourcesContainer) ListChangedChan() <-chan struct{} { return rc.notifier.Subscriber() }

// UpdatedChan returns a channel signaled when URI content/metadata changes.
func (rc *ResourcesContainer) UpdatedChan(uri string) <-chan struct{} {
	rc.mu.Lock()
	nt := rc.updatedNotifiers[uri]
	if nt == nil {
		nt = &ChangeNotifier{}
		rc.updatedNotifiers[uri] = nt
	}
	rc.mu.Unlock()
	return nt.Subscriber()
}

func (rc *ResourcesContainer) markUpdated(uri string) {
	rc.mu.RLock()
	nt := rc.updatedNotifiers[uri]
	rc.mu.RUnlock()
	if nt != nil {
		_ = nt.Notify(context.Background())
	}
}

// internal helpers ------------------------------------------------------------------

func normalizeResource(r Resource) Resource {
	// Defensive deep copy of variants & blob slices
	if len(r.Variants) > 0 {
		vs := make([]ContentVariant, len(r.Variants))
		for i, v := range r.Variants {
			if len(v.Blob) > 0 {
				cp := make([]byte, len(v.Blob))
				copy(cp, v.Blob)
				v.Blob = cp
			}
			vs[i] = v
		}
		r.Variants = vs
	}
	return r
}

func equalVariants(a, b []ContentVariant) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		av, bv := a[i], b[i]
		if av.Text != bv.Text || av.MimeType != bv.MimeType {
			return false
		}
		if len(av.Blob) != len(bv.Blob) {
			return false
		}
		for j := range av.Blob {
			if av.Blob[j] != bv.Blob[j] {
				return false
			}
		}
	}
	return true
}

// unsubscribeLocked assumes write lock held.
// (legacy subscription helper functions removed; engine owns subscription lifecycle)

// --- ResourcesCapability implementation -------------------------------------------

// ProvideResources implements ResourcesCapabilityProvider (self-providing static capability).
func (rc *ResourcesContainer) ProvideResources(ctx context.Context, session sessions.Session) (ResourcesCapability, bool, error) {
	if rc == nil {
		return nil, false, nil
	}
	return rc, true, nil
}

// ListResources paginates over static resources.
func (rc *ResourcesContainer) ListResources(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.Resource], error) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	start := parseCursor(cursor)
	if start >= len(rc.resources) {
		return NewPage[mcp.Resource](nil), nil
	}
	end := start + rc.pageSize
	if end > len(rc.resources) {
		end = len(rc.resources)
	}
	slice := rc.resources[start:end]
	out := make([]mcp.Resource, 0, len(slice))
	for _, r := range slice {
		out = append(out, mcp.Resource{URI: r.URI, Name: r.Name, Description: r.Desc, MimeType: r.MimeType})
	}
	page := NewPage(out)
	if end < len(rc.resources) {
		page.NextCursor = ptr(stringifyInt(end))
	}
	return page, nil
}

// ListResourceTemplates paginates over templates.
func (rc *ResourcesContainer) ListResourceTemplates(ctx context.Context, session sessions.Session, cursor *string) (Page[mcp.ResourceTemplate], error) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	start := parseCursor(cursor)
	if start >= len(rc.templates) {
		return NewPage[mcp.ResourceTemplate](nil), nil
	}
	end := start + rc.pageSize
	if end > len(rc.templates) {
		end = len(rc.templates)
	}
	slice := rc.templates[start:end]
	out := make([]mcp.ResourceTemplate, 0, len(slice))
	for _, t := range slice {
		out = append(out, mcp.ResourceTemplate{URITemplate: t.URITemplate, Name: t.Name, Description: t.Desc, MimeType: t.MimeType})
	}
	page := NewPage(out)
	if end < len(rc.templates) {
		page.NextCursor = ptr(stringifyInt(end))
	}
	return page, nil
}

// ReadResource returns contents variants for a URI translated to wire types.
func (rc *ResourcesContainer) ReadResource(ctx context.Context, session sessions.Session, uri string) ([]mcp.ResourceContents, error) {
	vs, ok := rc.Read(uri)
	if !ok {
		return nil, errors.New("resource not found: " + uri)
	}
	rc.mu.RLock()
	mimeDefault := rc.resources[rc.uriSet[uri]].MimeType
	rc.mu.RUnlock()
	out := make([]mcp.ResourceContents, 0, len(vs))
	for _, v := range vs {
		out = append(out, variantToWire(uri, v, mimeDefault))
	}
	return out, nil
}

// GetSubscriptionCapability intentionally returns (nil,false); engine synthesizes
// subscription support using UpdatedChan & ListChangedChan when it detects this
// container type (avoids duplicate subscription state across layers).
func (rc *ResourcesContainer) GetSubscriptionCapability(ctx context.Context, session sessions.Session) (ResourceSubscriptionCapability, bool, error) {
	return nil, false, nil
}

// GetListChangedCapability wires list changed notifications via the container's notifier.
func (rc *ResourcesContainer) GetListChangedCapability(ctx context.Context, session sessions.Session) (ResourceListChangedCapability, bool, error) {
	return resourceListChangedFromSubscriber{sub: &rc.notifier}, true, nil
}

// variantToWire converts a ContentVariant into an mcp.ResourceContents.
func variantToWire(uri string, v ContentVariant, mimeDefault string) mcp.ResourceContents {
	mime := v.MimeType
	if mime == "" {
		mime = mimeDefault
	}
	if v.Text != "" {
		return mcp.ResourceContents{URI: uri, MimeType: mime, Text: v.Text}
	}
	if len(v.Blob) > 0 {
		return mcp.ResourceContents{URI: uri, MimeType: mime, Blob: base64.StdEncoding.EncodeToString(v.Blob)}
	}
	// empty variant => empty text content
	return mcp.ResourceContents{URI: uri, MimeType: mime}
}

// Utility helpers -------------------------------------------------------------------

func stringifyInt(n int) string { return strconv.Itoa(n) }

// ptr helper (avoid redefining in multiple files); could be consolidated if already present elsewhere.
func ptr[T any](v T) *T { return &v }
