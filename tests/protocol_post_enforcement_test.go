package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
	"github.com/ggoodman/mcp-server-go/streaminghttp"
)

type noAuthProto2 struct{}

func (a *noAuthProto2) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
	return fakeUserInfo("enforce-user"), nil
}

// helper to spin up a test server + handler.
func setupTestServer(t *testing.T) (*httptest.Server, http.Handler) {
	t.Helper()
	ctx := t.Context()
	mh := memoryhost.New()
	srvCaps := mcpservice.NewServer()
	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	h, err := streaminghttp.New(ctx, srv.URL, mh, srvCaps, new(noAuthProto2), streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{Issuer: "http://127.0.0.1:0", JwksURI: "http://127.0.0.1/jwks.json"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	handler = h
	return srv, h
}

// perform initialize and return session id
func doInitialize(t *testing.T, srv *httptest.Server) string {
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.LatestProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "proto", "version": "1"},
		},
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("initialize post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status %d", resp.StatusCode)
	}
	sid := resp.Header.Get("mcp-session-id")
	if sid == "" {
		t.Fatalf("missing session id header")
	}
	return sid
}

// TestMissingProtocolHeaderOnRequest ensures non-initialize calls without header are rejected.
func TestMissingProtocolHeaderOnRequestAllowed(t *testing.T) {
	// Now that server infers protocol version from session, missing header should NOT 400.
	srv, _ := setupTestServer(t)
	defer srv.Close()
	sid := doInitialize(t, srv)

	reqBody := map[string]any{"jsonrpc": "2.0", "id": "2", "method": "ping"}
	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("mcp-session-id", sid)
	req.Header.Set("Accept", "text/event-stream") // ensure we test protocol fallback, not Accept enforcement
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { // request with ID yields SSE 200 OK now
		t.Fatalf("expected 200 OK with inferred protocol version, got %d", resp.StatusCode)
	}
	if pv := resp.Header.Get("MCP-Protocol-Version"); pv == "" {
		t.Fatalf("expected MCP-Protocol-Version header in response")
	}
}

// Ensure we still reject mismatch when header supplied and differs
func TestProtocolVersionMismatchStillRejected(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	sid := doInitialize(t, srv)

	reqBody := map[string]any{"jsonrpc": "2.0", "id": "3", "method": "ping"}
	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("mcp-session-id", sid)
	req.Header.Set("MCP-Protocol-Version", "2999-01-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on mismatch, got %d", resp.StatusCode)
	}
}

// Ensure Accept negotiation only enforced for SSE producing requests.
func TestAcceptHeaderOptionalForSSE(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	sid := doInitialize(t, srv)

	// notification (no ID) still accepted without Accept
	notif := map[string]any{"jsonrpc": "2.0", "method": "ping"}
	b, _ := json.Marshal(notif)
	nreq, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	nreq.Header.Set("Authorization", "Bearer x")
	nreq.Header.Set("Content-Type", "application/json")
	nreq.Header.Set("mcp-session-id", sid)
	nreq.Header.Set("MCP-Protocol-Version", mcp.LatestProtocolVersion)
	nresp, err := http.DefaultClient.Do(nreq)
	if err != nil {
		t.Fatalf("notif post: %v", err)
	}
	defer nresp.Body.Close()
	if nresp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 got %d", nresp.StatusCode)
	}

	// request with ID (SSE) without Accept now allowed
	reqBody := map[string]any{"jsonrpc": "2.0", "id": "77", "method": "ping"}
	b2, _ := json.Marshal(reqBody)
	sreq, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b2))
	sreq.Header.Set("Authorization", "Bearer x")
	sreq.Header.Set("Content-Type", "application/json")
	sreq.Header.Set("mcp-session-id", sid)
	sreq.Header.Set("MCP-Protocol-Version", mcp.LatestProtocolVersion)
	sresp, err := http.DefaultClient.Do(sreq)
	if err != nil {
		t.Fatalf("stream post: %v", err)
	}
	defer sresp.Body.Close()
	if sresp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 got %d", sresp.StatusCode)
	}
}

func TestBatchArrayReturnsJSONError(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	batch := []map[string]any{{"jsonrpc": "2.0", "id": 1, "method": "ping"}}
	b, _ := json.Marshal(batch)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d", resp.StatusCode)
	}
}

// TestSecondInitializeRejected ensures second initialize returns 409.
func TestSecondInitializeRejected(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	sid := doInitialize(t, srv)

	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "2",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.LatestProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "proto", "version": "1"},
		},
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("mcp-session-id", sid)
	req.Header.Set("MCP-Protocol-Version", mcp.LatestProtocolVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 second initialize, got %d", resp.StatusCode)
	}
}

// TestProtocolVersionMismatch ensures mismatch returns 400.
func TestProtocolVersionMismatch(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	sid := doInitialize(t, srv)

	reqBody := map[string]any{"jsonrpc": "2.0", "id": "2", "method": "ping"}
	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("mcp-session-id", sid)
	req.Header.Set("MCP-Protocol-Version", "2999-01-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 version mismatch, got %d", resp.StatusCode)
	}
}
