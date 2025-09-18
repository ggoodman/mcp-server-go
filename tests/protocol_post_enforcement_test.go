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
func TestMissingProtocolHeaderOnRequest(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	sid := doInitialize(t, srv)

	reqBody := map[string]any{"jsonrpc": "2.0", "id": "2", "method": "ping"}
	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("mcp-session-id", sid)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 missing protocol header, got %d", resp.StatusCode)
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
