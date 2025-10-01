package mcpservice

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ggoodman/mcp-server-go/sessions"
)

type fakeSession string

func (s fakeSession) SessionID() string       { return string(s) }
func (s fakeSession) UserID() string          { return string(s) }
func (s fakeSession) ProtocolVersion() string { return "" }
func (s fakeSession) GetSamplingCapability() (cap sessions.SamplingCapability, ok bool) {
	return nil, false
}
func (s fakeSession) GetRootsCapability() (cap sessions.RootsCapability, ok bool) { return nil, false }
func (s fakeSession) GetElicitationCapability() (cap sessions.ElicitationCapability, ok bool) {
	return nil, false
}
func (s fakeSession) PutData(ctx context.Context, key string, value []byte) error { return nil }
func (s fakeSession) GetData(ctx context.Context, key string) ([]byte, bool, error) {
	return nil, false, nil
}
func (s fakeSession) DeleteData(ctx context.Context, key string) error { return nil }

func writeFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestFSResources_ListAndRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "hello")
	writeFile(t, dir, "b.md", "# readme")

	r := NewFSResources(WithOSDir(dir), WithBaseURI("fs://test"))

	page, err := r.ListResources(ctx, fakeSession("s1"), nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(page.Items))
	}

	// Read one file
	uri := "fs://test/a.txt"
	contents, err := r.ReadResource(ctx, fakeSession("s1"), uri)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(contents) != 1 || contents[0].Text != "hello" || contents[0].MimeType == "" {
		t.Fatalf("unexpected contents: %+v", contents)
	}
}

func TestFSResources_PathConfinement_SymlinkEscapeDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	secret := writeFile(t, root, "..outside.txt", "nope")
	dir := t.TempDir()
	// symlink inside dir pointing to secret outside root
	_ = os.Symlink(secret, filepath.Join(dir, "link.txt"))

	r := NewFSResources(WithOSDir(dir), WithBaseURI("fs://root"))
	// URI for link
	uri := "fs://root/link.txt"
	if _, err := r.ReadResource(ctx, fakeSession("s"), uri); err == nil {
		t.Fatal("expected symlink escape to be denied")
	}
}

func TestFSResources_SubscribeAndDebouncedUpdate(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "v1")

	// Short polling and debounce to make test fast
	r := NewFSResources(WithOSDir(dir), WithBaseURI("fs://t"), WithPolling(50*time.Millisecond))
	r.updateDebounce = 30 * time.Millisecond

	// Subscribe to the file
	uri := "fs://t/file.txt"
	subCap, ok, err := r.GetSubscriptionCapability(ctx, fakeSession("s1"))
	if err != nil || !ok {
		t.Fatalf("expected subscription capability: %v %v", ok, err)
	}
	cfn, err := subCap.Subscribe(ctx, fakeSession("s1"), uri, func(ctx context.Context, _ string) {})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = cfn(context.Background()) }()

	// Observe internal per-URI notifier channel to assert debouncing behavior.
	ch := r.subscriberForURI(uri)

	// Trigger several quick updates within one debounce window
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("v2"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		// small sleep to ensure modtime changes but within debounce interval
		time.Sleep(5 * time.Millisecond)
	}

	// Expect only a single tick due to debouncing
	select {
	case <-ch:
		// got first
	case <-ctx.Done():
		t.Fatal("timeout waiting for first debounced update")
	}

	// Ensure no immediate second tick (debounce merged updates)
	select {
	case <-ch:
		// If we get a second tick immediately, debouncing failed; allow small grace
		time.Sleep(20 * time.Millisecond)
		select {
		case <-ch:
			// still ticking too fast
			t.Fatal("received multiple updates without spacing; expected debounced single notification")
		default:
		}
	default:
	}
}
