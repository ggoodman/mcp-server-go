package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ggoodman/mcp-server-go/examples/workspace_fs"
	"github.com/ggoodman/mcp-server-go/mcp"
)

func TestLib_WorkspaceFS_ListAndTools(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	// Seed a file
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := workspace_fs.New(dir)
	sess := fakeSession{id: "s1", user: "u1"}

	// Resources
	resCap, ok, err := srv.GetResourcesCapability(ctx, sess)
	if err != nil || !ok || resCap == nil {
		t.Fatalf("resources capability: ok=%v err=%v", ok, err)
	}
	page, err := resCap.ListResources(ctx, sess, nil)
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("list resources: %+v err=%v", page, err)
	}

	// Tools
	tools, ok, err := srv.GetToolsCapability(ctx, sess)
	if err != nil || !ok || tools == nil {
		t.Fatalf("tools capability: ok=%v err=%v", ok, err)
	}

	// read with uri from listing
	uri := page.Items[0].URI
	rr := callReq("fs.read", map[string]any{"uri": uri})
	rres, err := tools.CallTool(ctx, sess, &rr)
	if err != nil {
		t.Fatalf("fs.read: %v", err)
	}
	if len(rres.Content) == 0 || rres.Content[0].Type != "resource" || rres.Content[0].Resource == nil {
		t.Fatalf("expected embedded resource in result: %+v", rres)
	}

	// write new file
	wr := callReq("fs.write", map[string]any{"path": "b.txt", "content": "world"})
	wres, err := tools.CallTool(ctx, sess, &wr)
	if err != nil {
		t.Fatalf("fs.write: %v", err)
	}
	// Expect both a resource link and an embedded resource
	if len(wres.Content) < 2 {
		t.Fatalf("expected link and embedded resource, got: %+v", wres.Content)
	}
}

// callReq is a tiny helper to build a CallToolRequestReceived with JSON args.
func callReq(name string, args map[string]any) mcp.CallToolRequestReceived {
	b, _ := json.Marshal(args)
	return mcp.CallToolRequestReceived{Name: name, Arguments: b}
}
