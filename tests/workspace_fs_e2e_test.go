package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	streaminghttp "github.com/ggoodman/mcp-server-go"
	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/examples/workspace_fs"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type noAuthWS struct{}

func (a *noAuthWS) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
	return fakeUserInfo("user-1"), nil
}

func TestExamples_WorkspaceFS_E2E(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer srv.Close()

	mh := memoryhost.New()
	h, err := streaminghttp.New(
		ctx,
		srv.URL,
		mh,
		workspace_fs.New(dir),
		new(noAuthWS),
		streaminghttp.WithServerName("examples"),
		streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{
			Issuer:  "http://127.0.0.1:0",
			JwksURI: "http://127.0.0.1/.well-known/jwks.json",
		}),
	)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}
	handler = h

	client := sdk.NewClient(&sdk.Implementation{Name: "e2e", Version: "0.0.0"}, &sdk.ClientOptions{})
	transport := &sdk.StreamableClientTransport{
		Endpoint:   srv.URL + "/",
		HTTPClient: &http.Client{Transport: authRT{base: http.DefaultTransport}},
	}
	cs, err := client.Connect(ctx, transport, &sdk.ClientSessionOptions{})
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer cs.Close()

	// List resources from workspace root
	lr, err := cs.ListResources(ctx, &sdk.ListResourcesParams{})
	if err != nil || len(lr.Resources) == 0 {
		t.Fatalf("ListResources failed: %+v err=%v", lr, err)
	}

	// Call fs.write
	wres, err := cs.CallTool(ctx, &sdk.CallToolParams{
		Name: "fs.write",
		Arguments: map[string]any{
			"path":    "b.txt",
			"content": "world",
		},
	})
	if err != nil {
		t.Fatalf("CallTool fs.write failed: %v", err)
	}
	if len(wres.Content) < 1 {
		t.Fatalf("unexpected write result: %+v", wres)
	}

	// Call fs.read
	rres, err := cs.CallTool(ctx, &sdk.CallToolParams{
		Name:      "fs.read",
		Arguments: map[string]any{"path": "b.txt"},
	})
	if err != nil {
		t.Fatalf("CallTool fs.read failed: %v", err)
	}
	if len(rres.Content) == 0 {
		t.Fatalf("unexpected read result: %+v", rres)
	}
}
