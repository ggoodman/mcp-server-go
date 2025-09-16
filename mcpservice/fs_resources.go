package mcpservice

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/fsnotify/fsnotify"
	"github.com/ggoodman/mcp-server-go/mcp"

	// SubscriberForURI returns a channel that receives a signal each time the
	// given resource URI is updated. If the URI is unknown, it returns a closed
	// channel. This is primarily intended for internal coordination.
	"github.com/ggoodman/mcp-server-go/sessions"
)

// FSResources provides a concrete ResourcesCapability over a restricted slice of a
// filesystem. It can wrap either an os dir (preferred when you need to defend
// against symlink escape) or an arbitrary fs.FS (such as embed.FS).
//
// Security: When configured with an OS directory root, FSResources prevents
// traversal outside the configured root, even through symlinks. When configured
// with a generic fs.FS, symlinks are not followed and parent traversal is
// rejected.
type FSResources struct {
	mu sync.RWMutex

	// backing filesystem. When osRoot != "", this will be os.DirFS(osRoot).
	fsys   fs.FS
	osRoot string // absolute, symlink-evaluated root on disk (if set)

	// presentation
	baseURI  string // scheme prefix for resource URIs (e.g., "fs://workspace")
	pageSize int

	// list-changed notifications via polling
	pollInterval time.Duration // <= 0 disables
	notifier     ChangeNotifier
	pollOnce     sync.Once
	polling      atomic.Bool // true when poller running

	// subscriptions: uri <-> sessionID
	subsByURI     map[string]map[string]struct{}
	subsBySession map[string]map[string]struct{}

	// per-URI debounced update notifiers
	updateDebounce   time.Duration
	updatedNotifiers map[string]*ChangeNotifier
	debounceState    map[string]*debouncer
}

// FSOption configures FSResources.
type FSOption func(*FSResources)

// WithOSDir sets the root to an OS directory. The path must exist. Symlinks
// are resolved and reads are constrained to the resolved root.
func WithOSDir(root string) FSOption {
	return func(r *FSResources) {
		abs := root
		if !filepath.IsAbs(abs) {
			var err error
			abs, err = filepath.Abs(abs)
			if err == nil {
				root = abs
			}
		}
		real, err := filepath.EvalSymlinks(root)
		if err == nil {
			r.osRoot = real
			r.fsys = os.DirFS(real)
		} else {
			// Defer error to first use if root missing; keep as provided.
			r.osRoot = root
			r.fsys = os.DirFS(root)
		}
	}
}

// WithFS provides a generic fs.FS (e.g., embed.FS). Parent traversal is
// rejected and symlinks are not followed.
func WithFS(f fs.FS) FSOption { return func(r *FSResources) { r.fsys = f; r.osRoot = "" } }

// WithBaseURI sets the URI prefix used in Resource.URI, e.g. "fs://workspace".
// Defaults to "fs://".
func WithBaseURI(base string) FSOption {
	return func(r *FSResources) { r.baseURI = strings.TrimRight(base, "/") }
}

// WithFSPageSize sets the paging page size. Defaults to 50.
func WithFSPageSize(n int) FSOption {
	return func(r *FSResources) {
		if n > 0 {
			r.pageSize = n
		}
	}
}

// WithPolling enables list-changed notifications using naive polling at the
// provided interval. Polling starts lazily on the first Register call and stops
// when its context is canceled. Defaults to disabled.
func WithPolling(interval time.Duration) FSOption {
	return func(r *FSResources) { r.pollInterval = interval }
}

// WithUpdateDebounce configures the per-URI update debounce interval.
// Set to 0 to disable debouncing.
func WithUpdateDebounce(d time.Duration) FSOption {
	return func(r *FSResources) { r.updateDebounce = d }
}

// NewFSResources constructs a filesystem-backed resources capability.
func NewFSResources(opts ...FSOption) *FSResources {
	r := &FSResources{baseURI: "fs://", pageSize: 50, updateDebounce: 250 * time.Millisecond}
	for _, o := range opts {
		o(r)
	}
	r.subsByURI = make(map[string]map[string]struct{})
	r.subsBySession = make(map[string]map[string]struct{})
	r.updatedNotifiers = make(map[string]*ChangeNotifier)
	r.debounceState = make(map[string]*debouncer)
	return r
}

// ListResources implements ResourcesCapability.
func (r *FSResources) ListResources(ctx context.Context, _ sessions.Session, cursor *string) (Page[mcp.Resource], error) {
	if r.fsys == nil {
		return NewPage[mcp.Resource](nil), nil
	}
	all, err := r.scanFiles(ctx)
	if err != nil {
		return NewPage[mcp.Resource](nil), err
	}
	return pageSlice(all, r.pageSize, cursor), nil
}

// ListResourceTemplates implements ResourcesCapability.
func (r *FSResources) ListResourceTemplates(ctx context.Context, _ sessions.Session, cursor *string) (Page[mcp.ResourceTemplate], error) {
	// No templates provided by the raw filesystem view.
	return NewPage[mcp.ResourceTemplate](nil), nil
}

// ReadResource implements ResourcesCapability.
func (r *FSResources) ReadResource(ctx context.Context, _ sessions.Session, uri string) ([]mcp.ResourceContents, error) {
	if r.fsys == nil {
		return nil, fmt.Errorf("resource not found: %s", uri)
	}
	rel, ok := r.uriToRel(uri)
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", uri)
	}

	// Security: for OS-backed FS, resolve symlinks and ensure containment.
	if r.osRoot != "" {
		abs := filepath.Join(r.osRoot, filepath.FromSlash(rel))
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("resource not found: %s", uri)
		}
		if !within(real, r.osRoot) {
			return nil, fmt.Errorf("resource not found: %s", uri)
		}
		// Read via OS because generic fs.FS may not give us symlink-resolved view.
		data, err := os.ReadFile(real)
		if err != nil {
			return nil, fmt.Errorf("read failed: %w", err)
		}
		mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(real)))
		return []mcp.ResourceContents{r.contentsFor(uri, mt, data)}, nil
	}

	// Generic fs.FS path: reject any parent traversal.
	if !validFSPath(rel) {
		return nil, fmt.Errorf("resource not found: %s", uri)
	}
	data, err := fs.ReadFile(r.fsys, rel)
	if err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}
	mt := mime.TypeByExtension(strings.ToLower(path.Ext(rel)))
	return []mcp.ResourceContents{r.contentsFor(uri, mt, data)}, nil
}

func (r *FSResources) contentsFor(uri, mimeType string, data []byte) mcp.ResourceContents {
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if utf8.Valid(data) {
		return mcp.ResourceContents{URI: uri, MimeType: mimeType, Text: string(data)}
	}
	return mcp.ResourceContents{URI: uri, MimeType: mimeType, Blob: base64.StdEncoding.EncodeToString(data)}
}

// GetSubscriptionCapability implements ResourcesCapability. This filesystem
// implementation does not support per-URI subscriptions.
func (r *FSResources) GetSubscriptionCapability(ctx context.Context, _ sessions.Session) (ResourceSubscriptionCapability, bool, error) {
	return fsSubscription{r: r}, true, nil
}

// GetListChangedCapability implements ResourcesCapability. When polling is
// configured, we expose a listChanged capability powered by ChangeNotifier.
func (r *FSResources) GetListChangedCapability(ctx context.Context, _ sessions.Session) (ResourceListChangedCapability, bool, error) {
	// We expose listChanged if either fsnotify can be used (OS dir root) or polling is configured.
	if r.osRoot == "" && r.pollInterval <= 0 {
		return nil, false, nil
	}
	return fsListChanged{r: r}, true, nil
}

type fsListChanged struct{ r *FSResources }

func (f fsListChanged) Register(ctx context.Context, s sessions.Session, fn NotifyResourceChangeFunc) (bool, error) {
	if fn == nil {
		return false, nil
	}
	// Lazily start watcher/poller once per FSResources instance.
	f.r.pollOnce.Do(func() {
		if f.r.osRoot != "" {
			go f.r.runFsnotify(ctx)
		} else if f.r.pollInterval > 0 {
			go f.r.runPoller(ctx)
		}
	})
	ch := f.r.notifier.Subscriber()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				fn(ctx, s, "")
			}
		}
	}()
	return true, nil
}

func (r *FSResources) runPoller(ctx context.Context) {
	if !r.polling.CompareAndSwap(false, true) {
		return
	}
	defer r.polling.Store(false)

	// Prime last snapshot.
	lastSnap, _ := r.snapshot(ctx)
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			curSnap, err := r.snapshot(ctx)
			if err != nil {
				continue
			}
			// Detect list-changed and per-file updates
			changed := false
			for p, cur := range curSnap {
				if prev, ok := lastSnap[p]; ok {
					if !prev.eq(cur) {
						// existing file changed -> updated
						r.markUpdated(r.relToURI(p))
						changed = true
					}
				} else {
					// added file
					changed = true
				}
			}
			// removed files
			for p := range lastSnap {
				if _, ok := curSnap[p]; !ok {
					changed = true
				}
			}
			if changed {
				lastSnap = curSnap
				_ = r.notifier.Notify(ctx)
			}
		}
	}
}

// runFsnotify uses fsnotify to watch the OS directory tree rooted at osRoot.
// It falls back to emitting list-changed when adds/removes occur and marks per-file
// updates when writes are observed. This requires WithOSDir configuration.
func (r *FSResources) runFsnotify(ctx context.Context) {
	if r.osRoot == "" {
		return
	}
	if !r.polling.CompareAndSwap(false, true) {
		return
	}
	defer r.polling.Store(false)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Debug("fsnotify unavailable", slog.String("err", err.Error()))
		return
	}
	defer func() {
		// Best-effort watcher close; no actionable error handling path.
		_ = w.Close()
	}()

	// Recursively add all directories under the root.
	addDirs := func() error {
		return filepath.WalkDir(r.osRoot, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			return w.Add(p)
		})
	}
	if err := addDirs(); err != nil {
		slog.Debug("fsnotify add dirs failed", slog.String("err", err.Error()))
	}

	// Signal one listChanged on startup to normalize initial state.
	_ = r.notifier.Notify(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			// Maintain watcher on newly created directories.
			if ev.Op&fsnotify.Create == fsnotify.Create {
				// If directory, add watch; if file, it's a list change.
				fi, err := os.Stat(ev.Name)
				if err == nil && fi.IsDir() {
					_ = w.Add(ev.Name)
					_ = r.notifier.Notify(ctx)
					continue
				}
				_ = r.notifier.Notify(ctx)
			}
			if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				// Removal or rename -> list changed. Watcher on removed dirs is auto-removed.
				_ = r.notifier.Notify(ctx)
			}
			if ev.Op&(fsnotify.Write|fsnotify.Chmod) != 0 {
				// File content may have changed. Convert path to URI if under root.
				// Use symlink-resolved absolute path for safety.
				real := ev.Name
				if abs, err := filepath.Abs(real); err == nil {
					real = abs
				}
				if !within(real, r.osRoot) {
					continue
				}
				rel, err := filepath.Rel(r.osRoot, real)
				if err != nil {
					continue
				}
				uri := r.relToURI(filepath.ToSlash(rel))
				r.markUpdated(uri)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			slog.Debug("fsnotify error", slog.String("err", err.Error()))
		}
	}
}

// snapshot returns a map path -> file metadata for all visible files.
func (r *FSResources) snapshot(ctx context.Context) (map[string]fileMeta, error) {
	rows := make(map[string]fileMeta)
	if r.fsys == nil {
		return nil, errors.New("no fs configured")
	}
	err := fs.WalkDir(r.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable nodes
		}
		if d.IsDir() {
			return nil
		}
		// Skip symlinks in generic fs; os path handled by osRoot read path checks.
		if isSymlink(d) {
			return nil
		}
		if !validFSPath(p) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var sz int64
		var mt time.Time
		if info, e := d.Info(); e == nil {
			sz = info.Size()
			mt = info.ModTime()
		}
		rows[p] = fileMeta{size: sz, mod: mt}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

type fileMeta struct {
	size int64
	mod  time.Time
}

func (a fileMeta) eq(b fileMeta) bool { return a.size == b.size && a.mod.Equal(b.mod) }

func (r *FSResources) scanFiles(ctx context.Context) ([]mcp.Resource, error) {
	var out []mcp.Resource
	err := fs.WalkDir(r.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort listing
		}
		if d.IsDir() {
			return nil
		}
		if isSymlink(d) {
			return nil
		}
		if !validFSPath(p) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		uri := r.relToURI(p)
		mt := mime.TypeByExtension(strings.ToLower(path.Ext(p)))
		name := path.Base(p)
		out = append(out, mcp.Resource{URI: uri, Name: name, MimeType: mt})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URI < out[j].URI })
	return out, nil
}

func isSymlink(d fs.DirEntry) bool {
	if d == nil {
		return false
	}
	if d.Type()&fs.ModeSymlink != 0 {
		return true
	}
	// Some FS don't set Type; fall back to Info
	if info, err := d.Info(); err == nil {
		return info.Mode()&fs.ModeSymlink != 0
	}
	return false
}

func validFSPath(p string) bool {
	// fs.ValidPath requires clean, no leading slash, and no ".." segments.
	if !fs.ValidPath(p) {
		return false
	}
	// Defensive: reject Windows volume roots or weird schemes in p
	if strings.Contains(p, ":") {
		return false
	}
	return true
}

func (r *FSResources) relToURI(rel string) string {
	// rel is fs path using '/' separators. Encode path segments for safety.
	segs := strings.Split(rel, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	p := strings.Join(segs, "/")
	return r.baseURI + "/" + p
}

func (r *FSResources) uriToRel(uri string) (string, bool) {
	base := strings.TrimRight(r.baseURI, "/") + "/"
	if !strings.HasPrefix(uri, base) {
		return "", false
	}
	p := strings.TrimPrefix(uri, base)
	// Decode percent-escapes
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if dec, err := url.PathUnescape(s); err == nil {
			segs[i] = dec
		} else {
			return "", false
		}
	}
	rel := path.Clean(strings.Join(segs, "/"))
	if rel == "." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// pageSlice paginates a slice using an integer cursor (offset as decimal).
func pageSlice[T any](all []T, pageSize int, cursor *string) Page[T] {
	start := 0
	if cursor != nil && *cursor != "" {
		if n, err := strconv.Atoi(*cursor); err == nil && n >= 0 && n <= len(all) {
			start = n
		}
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	items := make([]T, end-start)
	copy(items, all[start:end])
	if end < len(all) {
		next := strconv.Itoa(end)
		return NewPage(items, WithNextCursor[T](next))
	}
	return NewPage(items)
}

// within returns true if target is the same as root or a descendant of root.
func within(target, root string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	return true
}

// --- Subscription and update notification helpers ---

type fsSubscription struct{ r *FSResources }

func (f fsSubscription) Subscribe(ctx context.Context, s sessions.Session, uri string) error {
	// Ensure the URI exists and is a file
	if !f.r.uriExists(ctx, uri) {
		return fmt.Errorf("resource not found: %s", uri)
	}
	// Ensure a watcher is running so updates are detected. Lazily start once.
	f.r.pollOnce.Do(func() {
		if f.r.osRoot != "" {
			go f.r.runFsnotify(context.Background())
		} else if f.r.pollInterval > 0 {
			go f.r.runPoller(context.Background())
		}
	})
	sid := s.SessionID()
	f.r.mu.Lock()
	if _, ok := f.r.subsByURI[uri]; !ok {
		f.r.subsByURI[uri] = make(map[string]struct{})
	}
	if _, ok := f.r.subsBySession[sid]; !ok {
		f.r.subsBySession[sid] = make(map[string]struct{})
	}
	f.r.subsByURI[uri][sid] = struct{}{}
	f.r.subsBySession[sid][uri] = struct{}{}
	f.r.mu.Unlock()
	return nil
}

func (f fsSubscription) Unsubscribe(ctx context.Context, s sessions.Session, uri string) error {
	sid := s.SessionID()
	f.r.mu.Lock()
	if m, ok := f.r.subsByURI[uri]; ok {
		delete(m, sid)
		if len(m) == 0 {
			delete(f.r.subsByURI, uri)
		}
	}
	if m, ok := f.r.subsBySession[sid]; ok {
		delete(m, uri)
		if len(m) == 0 {
			delete(f.r.subsBySession, sid)
		}
	}
	f.r.mu.Unlock()
	return nil
}

// uriExists returns true if the URI resolves to a regular file within the allowed root.
func (r *FSResources) uriExists(ctx context.Context, uri string) bool {
	rel, ok := r.uriToRel(uri)
	if !ok {
		return false
	}
	if r.osRoot != "" {
		abs := filepath.Join(r.osRoot, filepath.FromSlash(rel))
		real, err := filepath.EvalSymlinks(abs)
		if err != nil || !within(real, r.osRoot) {
			return false
		}
		st, err := os.Stat(real)
		if err != nil || st.IsDir() {
			return false
		}
		return true
	}
	if !validFSPath(rel) {
		return false
	}
	f, err := r.fsys.Open(rel)
	if err != nil {
		return false
	}
	defer func() {
		// Best-effort file close; writes already flushed or will surface earlier.
		_ = f.Close()
	}()
	if info, err := fs.Stat(r.fsys, rel); err == nil && info.Mode().IsRegular() {
		return true
	}
	return false
}

// markUpdated triggers a debounced update signal for a specific URI.
func (r *FSResources) markUpdated(uri string) {
	r.mu.Lock()
	db, ok := r.debounceState[uri]
	if !ok {
		if r.updatedNotifiers[uri] == nil {
			r.updatedNotifiers[uri] = &ChangeNotifier{}
		}
		db = &debouncer{interval: r.updateDebounce, fire: func() { _ = r.updatedNotifiers[uri].Notify(context.Background()) }}
		r.debounceState[uri] = db
	}
	r.mu.Unlock()
	db.trigger()
}

// subscriberForURI returns a channel that ticks when the given URI updates.
// Intended primarily for tests and internal wiring.
func (r *FSResources) subscriberForURI(uri string) <-chan struct{} {
	r.mu.Lock()
	if r.updatedNotifiers[uri] == nil {
		r.updatedNotifiers[uri] = &ChangeNotifier{}
	}
	n := r.updatedNotifiers[uri]
	r.mu.Unlock()
	return n.Subscriber()
}

// SubscriberForURI exposes a change subscriber channel for the given URI.
// It is used by the HTTP handler to bridge server-local update signals to
// client-facing notifications. Safe for concurrent use.
func (r *FSResources) SubscriberForURI(uri string) <-chan struct{} { return r.subscriberForURI(uri) }

type debouncer struct {
	mu       sync.Mutex
	timer    *time.Timer
	pending  bool
	interval time.Duration
	fire     func()
}

func (d *debouncer) trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.interval <= 0 {
		d.fire()
		return
	}
	if d.pending {
		return
	}
	d.pending = true
	if d.timer == nil {
		d.timer = time.AfterFunc(d.interval, d.flush)
	} else {
		d.timer.Reset(d.interval)
	}
}

func (d *debouncer) flush() {
	d.mu.Lock()
	d.pending = false
	d.mu.Unlock()
	d.fire()
}
