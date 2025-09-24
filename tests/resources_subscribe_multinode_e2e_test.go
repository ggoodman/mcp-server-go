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
	"time"

	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions/redishost"
	"github.com/ggoodman/mcp-server-go/streaminghttp"
)

type noAuthMN struct{}

func (a *noAuthMN) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
	return fakeUserInfo("user-mn"), nil
}

// This test requires a running Redis at localhost:6379. If unavailable, it is skipped.
func TestResources_SubscribeUpdated_MultiNode_UnsubscribeFanout(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Quick availability check
	h, err := redishost.New("localhost:6379", redishost.WithKeyPrefix("mcp:mn:test:"))
	if err != nil {
		t.Skipf("skipping multi-node test (no redis): %v", err)
		return
	}
	defer h.Close()

	// Shared StaticResources backing on handler A
	static := mcpservice.NewStaticResources(
		[]mcp.Resource{{URI: "res://x", Name: "x"}},
		nil,
		map[string][]mcp.ResourceContents{"res://x": {{URI: "res://x", Text: "v1"}}},
	)
	srvCapsA := mcpservice.NewServer(
		mcpservice.WithResourcesOptions(mcpservice.WithStaticResourceContainer(static)),
	)
	srvCapsB := mcpservice.NewServer(
		mcpservice.WithResourcesOptions(mcpservice.WithStaticResourceContainer(static)),
	)

	// Start two independent HTTP servers (A, B) sharing the same session host
	var handlerA http.Handler
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handlerA.ServeHTTP(w, r) }))
	defer srvA.Close()
	var handlerB http.Handler
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handlerB.ServeHTTP(w, r) }))
	defer srvB.Close()

	ha, err := streaminghttp.New(ctx, srvA.URL, h, srvCapsA, new(noAuthMN), streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{Issuer: "http://127.0.0.1:0", JwksURI: "http://127.0.0.1/jwks.json"}))
	if err != nil {
		t.Fatalf("handler A: %v", err)
	}
	handlerA = ha

	hb, err := streaminghttp.New(ctx, srvB.URL, h, srvCapsB, new(noAuthMN), streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{Issuer: "http://127.0.0.1:0", JwksURI: "http://127.0.0.1/jwks.json"}))
	if err != nil {
		t.Fatalf("handler B: %v", err)
	}
	handlerB = hb

	// Initialize session via POST to A; capture session id
	initBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.LatestProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "mn", "version": "0"},
		},
	}
	b, _ := json.Marshal(initBody)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srvA.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("initialize A: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("init status: %d", resp.StatusCode)
	}
	sessID := resp.Header.Get("mcp-session-id")
	resp.Body.Close()
	if sessID == "" {
		t.Fatalf("missing session id")
	}

	// Attach GET stream to B (demonstrates cross-node delivery)
	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		greq, _ := http.NewRequestWithContext(ctx, http.MethodGet, srvB.URL+"/", nil)
		greq.Header.Set("Authorization", "Bearer x")
		greq.Header.Set("Accept", "text/event-stream")
		greq.Header.Set("mcp-session-id", sessID)
		r, e := http.DefaultClient.Do(greq)
		if e != nil {
			errCh <- e
			return
		}
		respCh <- r
	}()
	var getResp *http.Response
	select {
	case e := <-errCh:
		t.Fatalf("GET B: %v", e)
	case getResp = <-respCh:
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for GET headers on B")
	}
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET B status: %d", getResp.StatusCode)
	}

	// Subscribe via POST to A for res://x
	subBody := map[string]any{"jsonrpc": "2.0", "id": "2", "method": string(mcp.ResourcesSubscribeMethod), "params": map[string]any{"uri": "res://x"}}
	bb, _ := json.Marshal(subBody)
	sreq, _ := http.NewRequestWithContext(ctx, http.MethodPost, srvA.URL+"/", bytes.NewReader(bb))
	sreq.Header.Set("Authorization", "Bearer x")
	sreq.Header.Set("Content-Type", "application/json")
	sreq.Header.Set("mcp-session-id", sessID)
	sresp, err := http.DefaultClient.Do(sreq)
	if err != nil {
		t.Fatalf("subscribe A: %v", err)
	}
	sresp.Body.Close()

	// Trigger update on A
	static.ReplaceAllContents(ctx, map[string][]mcp.ResourceContents{"res://x": {{URI: "res://x", Text: "v2"}}})

	// Expect resources/updated on B
	scanner := bufio.NewScanner(getResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	var gotUpdated bool
	for !gotUpdated {
		select {
		case <-deadline.C:
			getResp.Body.Close()
			t.Fatalf("timeout waiting for updated on B")
		default:
		}
		if !scanner.Scan() {
			time.Sleep(10 * time.Millisecond)
			continue
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
			gotUpdated = true
		}
	}

	// Unsubscribe via POST to B
	unsubBody := map[string]any{"jsonrpc": "2.0", "id": "3", "method": string(mcp.ResourcesUnsubscribeMethod), "params": map[string]any{"uri": "res://x"}}
	ub, _ := json.Marshal(unsubBody)
	ureq, _ := http.NewRequestWithContext(ctx, http.MethodPost, srvB.URL+"/", bytes.NewReader(ub))
	ureq.Header.Set("Authorization", "Bearer x")
	ureq.Header.Set("Content-Type", "application/json")
	ureq.Header.Set("mcp-session-id", sessID)
	uresp, err := http.DefaultClient.Do(ureq)
	if err != nil {
		t.Fatalf("unsubscribe B: %v", err)
	}
	uresp.Body.Close()

	// Trigger another update on A and ensure no more updates are received on B shortly thereafter
	static.ReplaceAllContents(ctx, map[string][]mcp.ResourceContents{"res://x": {{URI: "res://x", Text: "v3"}}})
	short := time.NewTimer(250 * time.Millisecond)
	defer short.Stop()
	for {
		select {
		case <-short.C:
			getResp.Body.Close()
			return
		default:
		}
		if !scanner.Scan() {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if line := scanner.Text(); strings.HasPrefix(line, "data: ") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			var m map[string]any
			if err := json.Unmarshal([]byte(payload), &m); err == nil {
				if method, _ := m["method"].(string); method == string(mcp.ResourcesUpdatedNotificationMethod) {
					getResp.Body.Close()
					t.Fatalf("received updated after unsubscribe on B")
				}
			}
		}
	}
}
