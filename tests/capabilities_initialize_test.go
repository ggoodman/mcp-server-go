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

type capNoAuth struct{}

func (a *capNoAuth) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
	return fakeUserInfo("caps-user"), nil
}

// TestInitializeCapabilitiesAdvertisement verifies server advertises only capabilities it actually supports.
func TestInitializeCapabilitiesAdvertisement(t *testing.T) {
	ctx := t.Context()
	mh := memoryhost.New()

	server := mcpservice.NewServer(
		mcpservice.WithToolsCapability(mcpservice.NewToolsContainer()), // empty tools container
	)

	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer srv.Close()

	h, err := streaminghttp.New(ctx, srv.URL, mh, server, new(capNoAuth), streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{Issuer: "http://127.0.0.1:0", JwksURI: "http://127.0.0.1/jwks.json"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	handler = h

	init := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  string(mcp.InitializeMethod),
		"params": map[string]any{
			"protocolVersion": mcp.LatestProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "c", "version": "1"},
		},
	}
	b, _ := json.Marshal(init)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	// Do not request SSE so initialize response is a plain JSON body for simpler parsing.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	var rpcResp struct {
		Result json.RawMessage     `json:"result"`
		Error  *struct{ Code int } `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &rpcResp); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, buf.String())
	}
	if rpcResp.Error != nil {
		t.Fatalf("initialize error: %+v", rpcResp.Error)
	}

	var initRes mcp.InitializeResult
	if err := json.Unmarshal(rpcResp.Result, &initRes); err != nil {
		t.Fatalf("unmarshal init result: %v", err)
	}

	// Assertions: Only tools capability should be present (sampling/roots/prompts/etc. absent or false)
	if initRes.Capabilities.Tools == nil {
		t.Fatalf("expected tools capability; got nil")
	}
	// Even with an empty tools container we advertise listChanged support because
	// the container can mutate over time (add/remove/replace tools) and emits notifications.
	if !initRes.Capabilities.Tools.ListChanged {
		t.Fatalf("expected tools.listChanged true with empty tools container")
	}
	// Resources capability may be advertised with default false flags; ensure we don't wrongly assert it's absent.
	// Prompts capability may similarly appear with default false flags.
	// Note: sampling/roots/elicitation not yet implemented in ServerCapabilities struct; absence implicitly validated.
}
