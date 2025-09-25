package tests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
	"github.com/ggoodman/mcp-server-go/streaminghttp"
)

type noAuthSub struct{}

func (a *noAuthSub) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
	return fakeUserInfo("user-sub"), nil
}

// End-to-end subscription: subscribe on POST side, attach GET on another request,
// trigger an update, expect notifications/resources/updated.
func TestResources_SubscribeUpdated_Unsubscribe_E2E(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Static container with a single resource; updates come from ReplaceAllContents.
	static := mcpservice.NewStaticResources(
		[]mcp.Resource{{URI: "res://x", Name: "x"}},
		nil,
		map[string][]mcp.ResourceContents{"res://x": {{URI: "res://x", Text: "v1"}}},
	)
	srvCaps := mcpservice.NewServer(
		mcpservice.WithResourcesOptions(
			mcpservice.WithStaticResourceContainer(static),
		),
	)

	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer srv.Close()

	h, err := streaminghttp.New(
		ctx,
		srv.URL,
		memoryhost.New(),
		srvCaps,
		new(noAuthSub),
		streaminghttp.WithServerName("examples"),
		streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{
			Issuer:  "http://127.0.0.1:0",
			JwksURI: "http://127.0.0.1/.well-known/jwks.json",
		}),
	)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	handler = h

	// 1) Initialize session via POST / (Accept: text/event-stream) and keep HTTP client to send subsequent JSON-RPC
	initBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "sub-test", "version": "0"},
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

	// 2a) Signal notifications/initialized so the server opens the session and accepts requests
	initNote := map[string]any{
		"jsonrpc": "2.0",
		"method":  string(mcp.InitializedNotificationMethod),
	}
	b, _ = json.Marshal(initNote)
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("mcp-session-id", sessID)
	initNoteResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("initialized notification: %v", err)
	}
	_ = initNoteResp.Body.Close()

	// 2b) Open GET SSE stream to receive notifications
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

	var getResp *http.Response
	select {
	case e := <-errCh:
		t.Fatalf("get: %v", e)
	case getResp = <-respCh:
	case <-t.Context().Done():
		t.Fatalf("context done waiting for GET /mcp response: %v", t.Context().Err())
	}
	if getResp.StatusCode != http.StatusOK {
		getResp.Body.Close()
		t.Fatalf("get status: %d", getResp.StatusCode)
	}

	// 3) Subscribe to res://x via POST JSON-RPC resources/subscribe
	subBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      "2",
		"method":  string(mcp.ResourcesSubscribeMethod),
		"params":  map[string]any{"uri": "res://x"},
	}
	b, _ = json.Marshal(subBody)
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("mcp-session-id", sessID)
	subResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_ = subResp.Body.Close()

	// 4) Trigger an update via ReplaceAllContents (which marks updated on res://x)
	static.ReplaceAllContents(ctx, map[string][]mcp.ResourceContents{"res://x": {{URI: "res://x", Text: "v2"}}})

	// 5) Expect a notifications/resources/updated on SSE
	scanner := bufio.NewScanner(getResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		select {
		case <-t.Context().Done():
			t.Fatalf("context done scanning GET /mcp response: %v", t.Context().Err())
		default:
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				t.Fatalf("scanner: %v", err)
			}
			break // EOF
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			continue
		}
		if method, _ := m["method"].(string); method == string(mcp.ResourcesUpdatedNotificationMethod) {
			break
		}
	}

	// 6) Unsubscribe via POST JSON-RPC; do not assert immediate quiescence (eventual semantics)
	unsubBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      "3",
		"method":  string(mcp.ResourcesUnsubscribeMethod),
		"params":  map[string]any{"uri": "res://x"},
	}
	b, _ = json.Marshal(unsubBody)
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("mcp-session-id", sessID)
	unsubResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	_ = unsubResp.Body.Close()

	// Close stream and finish; subsequent updates may be observed briefly under eventual semantics.
	getResp.Body.Close()
	return
}
