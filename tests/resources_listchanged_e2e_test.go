package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
	"github.com/ggoodman/mcp-server-go/streaminghttp"
)

type noAuthLC struct{}

func (a *noAuthLC) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
	return fakeUserInfo("user-lc"), nil
}

func TestResources_ListChanged_E2E(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// Build a server with a ResourcesContainer. The container automatically
	// emits list-changed notifications on mutations (add/remove/replace), and
	// the resources capability advertises listChanged support when a container
	// is used—no explicit ChangeNotifier wiring required.
	static := mcpservice.NewResourcesContainer(
		[]mcp.Resource{{URI: "res://a", Name: "a"}},
		nil,
		map[string][]mcp.ResourceContents{"res://a": {{URI: "res://a", Text: "A"}}},
	)
	srvCaps := mcpservice.NewServer(
		mcpservice.WithResourcesCapability(static),
	)

	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer srv.Close()

	h, err := streaminghttp.New(
		ctx,
		srv.URL,
		memoryhost.New(),
		srvCaps,
		new(noAuthLC),
		streaminghttp.WithServerName("examples"),
		streaminghttp.WithSecurityConfig(auth.SecurityConfig{Issuer: "http://127.0.0.1:0", Audiences: []string{"test"}, JWKSURL: "http://127.0.0.1/.well-known/jwks.json", Advertise: true}),
	)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	handler = h

	// 1) Initialize session via POST /mcp (Accept: text/event-stream)
	initBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "lc-test", "version": "0"},
		},
	}
	b, _ := json.Marshal(initBody)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status: %d", resp.StatusCode)
	}
	sessID := resp.Header.Get("mcp-session-id")
	if sessID == "" {
		t.Fatalf("missing session id")
	}
	_ = resp.Body.Close()

	// 2) Open GET SSE stream in a goroutine so Notify can unblock header write
	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		getReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/", nil)
		getReq.Header.Set("Accept", "text/event-stream")
		getReq.Header.Set("Authorization", "Bearer test-token")
		getReq.Header.Set("mcp-session-id", sessID)
		r, e := http.DefaultClient.Do(getReq)
		if e != nil {
			errCh <- e
			return
		}
		respCh <- r
	}()

	// 3) Give the GET handler a brief moment to subscribe and register publishers, then trigger list-changed
	time.Sleep(150 * time.Millisecond)
	// Trigger list-changed by mutating the static set (auto-notifies)
	_ = static.AddResource(ctx, mcp.Resource{URI: "res://b", Name: "b"})

	var getResp *http.Response
	select {
	case e := <-errCh:
		t.Fatalf("get: %v", e)
	case getResp = <-respCh:
	case <-t.Context().Done():
		t.Fatalf("context done waiting for GET response headers: %v", t.Context().Err())
	}
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status: %d", getResp.StatusCode)
	}

	// 4) Wait (bounded) for resources/list_changed notification
	if err := waitForNotification(t.Context(), getResp.Body, string(mcp.ResourcesListChangedNotificationMethod), 8*time.Second); err != nil {
		getResp.Body.Close()
		t.Fatalf("waiting for list_changed: %v", err)
	}
	getResp.Body.Close()
}
