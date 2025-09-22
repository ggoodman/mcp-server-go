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

type noAuthProto struct{}

func (a *noAuthProto) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
	return fakeUserInfo("proto-user"), nil
}

// TestInitializeHandshake spec expectations:
// - Single JSON-RPC request (no array batching)
// - Method initialize
// - Echo / negotiate protocol version (server may override but we use same)
// - Returns capabilities object and serverInfo
// - Sets mcp-session-id and MCP-Protocol-Version headers
func TestInitializeHandshake(t *testing.T) {
	ctx := t.Context()
	mh := memoryhost.New()
	srvCaps := mcpservice.NewServer()

	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer srv.Close()

	h, err := streaminghttp.New(ctx, srv.URL, mh, srvCaps, new(noAuthProto), streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{Issuer: "http://127.0.0.1:0", JwksURI: "http://127.0.0.1/jwks.json"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	handler = h

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
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	if resp.Header.Get("mcp-session-id") == "" {
		t.Fatalf("missing session id header")
	}
	if resp.Header.Get("MCP-Protocol-Version") == "" {
		t.Fatalf("missing MCP-Protocol-Version header")
	}
}

// TestRejectBatchArray ensures we reject JSON-RPC batch arrays per streaming transport rules.
func TestRejectBatchArray(t *testing.T) {
	ctx := t.Context()
	mh := memoryhost.New()
	srvCaps := mcpservice.NewServer()
	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer srv.Close()

	h, err := streaminghttp.New(ctx, srv.URL, mh, srvCaps, new(noAuthProto), streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{Issuer: "http://127.0.0.1:0", JwksURI: "http://127.0.0.1/jwks.json"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	handler = h

	batch := []map[string]any{{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": mcp.LatestProtocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "p", "version": "1"}}}}
	b, _ := json.Marshal(batch)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected non-200 for batch array")
	}
}
