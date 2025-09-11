package tests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	streaminghttp "github.com/ggoodman/mcp-server-go"
	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpserver"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
)

type noAuthDel struct{}

func (a *noAuthDel) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
	return fakeUserInfo("user-del"), nil
}

func TestDeleteSession_ClosesStreamsAndRevokes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	mh := memoryhost.New()
	// Minimal server capabilities
	srvCaps := mcpserver.NewServer()

	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer srv.Close()

	h, err := streaminghttp.New(
		ctx,
		srv.URL,
		mh,
		srvCaps,
		new(noAuthDel),
		streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{Issuer: "http://127.0.0.1:0", JwksURI: "http://127.0.0.1/jwks.json"}),
	)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	handler = h

	// Initialize to obtain a session id
	initBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "del", "version": "1"}},
	}
	b, _ := json.Marshal(initBody)
	preq, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	preq.Header.Set("Authorization", "Bearer x")
	preq.Header.Set("Content-Type", "application/json")
	preq.Header.Set("Accept", "text/event-stream")
	presp, err := http.DefaultClient.Do(preq)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if presp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status: %d", presp.StatusCode)
	}
	sessID := presp.Header.Get("mcp-session-id")
	presp.Body.Close()
	if sessID == "" {
		t.Fatalf("missing session id")
	}

	// Open GET stream
	gctx, cancel := context.WithCancel(ctx)
	defer cancel()
	greq, _ := http.NewRequestWithContext(gctx, http.MethodGet, srv.URL+"/", nil)
	greq.Header.Set("Authorization", "Bearer x")
	greq.Header.Set("Accept", "text/event-stream")
	greq.Header.Set("mcp-session-id", sessID)
	greq.Close = true
	gresp, err := http.DefaultClient.Do(greq)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if gresp.StatusCode != http.StatusOK {
		t.Fatalf("get status: %d", gresp.StatusCode)
	}
	// Start scanning until closed
	scanDone := make(chan struct{})
	scanner := bufio.NewScanner(gresp.Body)
	go func() {
		for scanner.Scan() {
			// drain
		}
		close(scanDone)
	}()

	// Give the server a moment to establish subscriptions
	time.Sleep(100 * time.Millisecond)

	// Issue DELETE /mcp to terminate the session
	dreq, _ := http.NewRequestWithContext(ctx, http.MethodDelete, srv.URL+"/", nil)
	dreq.Header.Set("Authorization", "Bearer x")
	dreq.Header.Set("mcp-session-id", sessID)
	dresp, err := http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status: %d", dresp.StatusCode)
	}
	dresp.Body.Close()

	// The GET stream should close soon after deletion
	select {
	case <-scanDone:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatalf("GET stream did not close after delete")
	}

	// Subsequent POST with same session should now fail with 404
	pingReq := map[string]any{"jsonrpc": "2.0", "id": "2", "method": string(mcp.PingMethod)}
	pb, _ := json.Marshal(pingReq)
	post, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/", bytes.NewReader(pb))
	post.Header.Set("Authorization", "Bearer x")
	post.Header.Set("Content-Type", "application/json")
	post.Header.Set("Accept", "text/event-stream")
	post.Header.Set("mcp-session-id", sessID)
	pResp, err := http.DefaultClient.Do(post)
	if err != nil {
		t.Fatalf("post after delete: %v", err)
	}
	if pResp.StatusCode != http.StatusNotFound {
		pResp.Body.Close()
		t.Fatalf("post-after-delete status: %d", pResp.StatusCode)
	}
	pResp.Body.Close()

	// Second DELETE should return 404 (already deleted / invalid)
	dreq2, _ := http.NewRequestWithContext(ctx, http.MethodDelete, srv.URL+"/", nil)
	dreq2.Header.Set("Authorization", "Bearer x")
	dreq2.Header.Set("mcp-session-id", sessID)
	dresp2, err := http.DefaultClient.Do(dreq2)
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if dresp2.StatusCode != http.StatusNotFound {
		dresp2.Body.Close()
		t.Fatalf("second delete status: %d", dresp2.StatusCode)
	}
	dresp2.Body.Close()
}
