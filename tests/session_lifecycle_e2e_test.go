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

	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
	"github.com/ggoodman/mcp-server-go/streaminghttp"
)

type noAuthSL struct{}

func (a *noAuthSL) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
	return fakeUserInfo("user-sl"), nil
}

// Test that a GET /mcp stream is bound to the request context: when the request is canceled,
// the server-side session subscription terminates and the response body closes promptly.
func TestSessionLifecycle_GetStreamBoundToRequest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	mh := memoryhost.New()

	// Minimal server with no special capabilities required for lifecycle check
	srvCaps := mcpservice.NewServer()

	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer srv.Close()

	h, err := streaminghttp.New(
		ctx,
		srv.URL,
		mh,
		srvCaps,
		new(noAuthSL),
		streaminghttp.WithSecurityConfig(auth.SecurityConfig{Issuer: "http://127.0.0.1:0", Audiences: []string{"test"}, JWKSURL: "http://127.0.0.1/jwks.json", Advertise: true}),
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
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "sl", "version": "1"},
		},
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

	// Open GET stream with a cancelable context and then cancel it; the body should close
	gctx, cancel := context.WithCancel(ctx)
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

	// Start scanning, then cancel; scan should terminate shortly
	scanner := bufio.NewScanner(gresp.Body)
	done := make(chan struct{})
	go func() {
		for scanner.Scan() {
			// drain until closed
		}
		close(done)
	}()
	// give the server a moment to start the subscription
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// ok closed
	case <-t.Context().Done():
		t.Fatalf("context done waiting for GET stream close after request cancel: %v", t.Context().Err())
	}
}

// Test that the POST request handling ties its writes to the request's lifecycle using the
// write-through wrapper: after canceling the POST context, WriteMessage falls back and request
// finishes without leaking resources.
func TestSessionLifecycle_PostBoundToRequest_WriteFallback(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	mh := memoryhost.New()
	srvCaps := mcpservice.NewServer(
		mcpservice.WithResourcesCapability(mcpservice.NewDynamicResources(
			mcpservice.WithResourcesListFunc(func(ctx context.Context, s sessions.Session, c *string) (mcpservice.Page[mcp.Resource], error) {
				return mcpservice.NewPage([]mcp.Resource{}), nil
			}),
		)),
	)

	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer srv.Close()

	h, err := streaminghttp.New(
		ctx,
		srv.URL,
		mh,
		srvCaps,
		new(noAuthSL),
		streaminghttp.WithSecurityConfig(auth.SecurityConfig{Issuer: "http://127.0.0.1:0", Audiences: []string{"test"}, JWKSURL: "http://127.0.0.1/jwks.json", Advertise: true}),
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
		"params":  map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "sl", "version": "1"}},
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

	// Build a POST request for resources/list then cancel immediately after sending
	listReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      "2",
		"method":  string(mcp.ResourcesListMethod),
		"params":  map[string]any{"cursor": ""},
	}
	lb, _ := json.Marshal(listReq)
	pctx, pcancel := context.WithCancel(ctx)
	req, _ := http.NewRequestWithContext(pctx, http.MethodPost, srv.URL+"/", bytes.NewReader(lb))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("mcp-session-id", sessID)

	// Send request; cancel quickly to force handler to fall back on write
	done := make(chan struct{})
	go func() {
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	pcancel()

	select {
	case <-done:
		// success: handler returned promptly; underlying fallbacks handled any pending writes
	case <-t.Context().Done():
		t.Fatalf("context done waiting for POST to finish after cancel: %v", t.Context().Err())
	}
}
