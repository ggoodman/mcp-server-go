package streaminghttp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
	"github.com/ggoodman/mcp-server-go/streaminghttp"
)

// --- test-only capability & server implementations for direct writer test ---
type testToolsCap struct{}

func (c testToolsCap) ListTools(ctx context.Context, session sessions.Session, cursor *string) (mcpservice.Page[mcp.Tool], error) {
	if rc, ok := session.GetRootsCapability(); ok {
		_, _ = rc.ListRoots(ctx) // trigger clientCall; ignore errors
	}
	return mcpservice.NewPage([]mcp.Tool{{Name: "dummy", Description: "d"}}), nil
}
func (c testToolsCap) CallTool(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{Content: []mcp.ContentBlock{}}, nil
}
func (c testToolsCap) GetListChangedCapability(ctx context.Context, s sessions.Session) (mcpservice.ToolListChangedCapability, bool, error) {
	return nil, false, nil
}

type testServer struct{ tools testToolsCap }

var _ mcpservice.ServerCapabilities = (*testServer)(nil)

func (ts *testServer) GetServerInfo(ctx context.Context, session sessions.Session) (mcp.ImplementationInfo, error) {
	return mcp.ImplementationInfo{Name: "test", Version: "0.0.1"}, nil
}
func (ts *testServer) GetPreferredProtocolVersion(ctx context.Context) (string, bool, error) {
	return "2025-06-18", true, nil
}
func (ts *testServer) GetInstructions(ctx context.Context, session sessions.Session) (string, bool, error) {
	return "", false, nil
}
func (ts *testServer) GetResourcesCapability(ctx context.Context, session sessions.Session) (mcpservice.ResourcesCapability, bool, error) {
	return nil, false, nil
}
func (ts *testServer) GetToolsCapability(ctx context.Context, session sessions.Session) (mcpservice.ToolsCapability, bool, error) {
	return ts.tools, true, nil
}
func (ts *testServer) GetPromptsCapability(ctx context.Context, session sessions.Session) (mcpservice.PromptsCapability, bool, error) {
	return nil, false, nil
}
func (ts *testServer) GetLoggingCapability(ctx context.Context, session sessions.Session) (mcpservice.LoggingCapability, bool, error) {
	return nil, false, nil
}
func (ts *testServer) GetCompletionsCapability(ctx context.Context, session sessions.Session) (mcpservice.CompletionsCapability, bool, error) {
	return nil, false, nil
}

func TestSingleInstance(t *testing.T) {
	t.Run("Initialize returns session and capabilities", func(t *testing.T) {
		// Explicit minimal server with empty static tools
		server := mcpservice.NewServer(
			mcpservice.WithToolsOptions(
				mcpservice.WithStaticToolsContainer(mcpservice.NewStaticTools()),
			),
		)
		srv := mustServer(t, server)
		defer srv.Close()

		// Build initialize request
		initReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.InitializeMethod),
			Params: func() json.RawMessage {
				p, _ := json.Marshal(mcp.InitializeRequest{
					ProtocolVersion: "2025-06-18",
					Capabilities:    mcp.ClientCapabilities{},
					ClientInfo: mcp.ImplementationInfo{
						Name:    "test-client",
						Version: "1.0.0",
						Title:   "Test Client",
					},
				})
				return p
			}(),
			ID: jsonrpc.NewRequestID("1"),
		}

		resp, evt := mustPostMCP(t, srv, "Bearer test-token", "", initReq)
		defer resp.Body.Close()

		if want, got := http.StatusOK, resp.StatusCode; want != got {
			t.Fatalf("unexpected status: want %d got %d", want, got)
		}

		sessID := resp.Header.Get("mcp-session-id")
		if sessID == "" {
			t.Fatalf("missing mcp-session-id header")
		}

		// Parse JSON-RPC response result
		var res jsonrpc.Response
		mustUnmarshalJSON(t, evt.data, &res)
		if res.Error != nil {
			t.Fatalf("initialize error: %+v", res.Error)
		}
		var initRes mcp.InitializeResult
		mustUnmarshalJSON(t, res.Result, &initRes)

		// Tools should be advertised (empty static container exposes listChanged)
		if initRes.Capabilities.Tools == nil || !initRes.Capabilities.Tools.ListChanged {
			t.Fatalf("expected tools listChanged capability to be true, got %#v", initRes.Capabilities.Tools)
		}
	})

	t.Run("Resources templates list over POST", func(t *testing.T) {
		server := mcpservice.NewServer(
			mcpservice.WithResourcesOptions(
				mcpservice.WithListResourceTemplates(func(_ context.Context, _ sessions.Session, _ *string) (mcpservice.Page[mcp.ResourceTemplate], error) {
					return mcpservice.NewPage([]mcp.ResourceTemplate{{URITemplate: "file://{path}", Name: "file"}}), nil
				}),
			),
		)
		srv := mustServer(t, server)
		defer srv.Close()

		// Initialize to get a session
		initReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.InitializeMethod),
			Params:         mustJSON(mcp.InitializeRequest{ProtocolVersion: "2025-06-18", ClientInfo: mcp.ImplementationInfo{Name: "c", Version: "1"}}),
			ID:             jsonrpc.NewRequestID(1),
		}
		resp, _ := mustPostMCP(t, srv, "Bearer test-token", "", initReq)
		sessID := resp.Header.Get("mcp-session-id")
		resp.Body.Close()

		// List resource templates
		listReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.ResourcesTemplatesListMethod),
			Params:         mustJSON(mcp.ListResourceTemplatesRequest{}),
			ID:             jsonrpc.NewRequestID(2),
		}
		resp2, evt := mustPostMCP(t, srv, "Bearer test-token", sessID, listReq)
		defer resp2.Body.Close()

		if want, got := http.StatusOK, resp2.StatusCode; want != got {
			t.Fatalf("unexpected status: want %d got %d", want, got)
		}
		var rpcRes jsonrpc.Response
		mustUnmarshalJSON(t, evt.data, &rpcRes)
		if rpcRes.Error != nil {
			t.Fatalf("resources/templates/list error: %+v", rpcRes.Error)
		}
		var listRes mcp.ListResourceTemplatesResult
		mustUnmarshalJSON(t, rpcRes.Result, &listRes)
		if want, got := 1, len(listRes.ResourceTemplates); want != got {
			t.Fatalf("unexpected templates length: want %d got %d", want, got)
		}
		if listRes.ResourceTemplates[0].URITemplate == "" {
			t.Fatalf("expected non-empty URITemplate")
		}
	})

	t.Run("Tools list over POST is empty", func(t *testing.T) {
		server := mcpservice.NewServer(
			mcpservice.WithToolsOptions(
				mcpservice.WithStaticToolsContainer(mcpservice.NewStaticTools()),
			),
		)
		srv := mustServer(t, server)
		defer srv.Close()

		// Initialize to get a session
		initReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.InitializeMethod),
			Params:         mustJSON(mcp.InitializeRequest{ProtocolVersion: "2025-06-18", ClientInfo: mcp.ImplementationInfo{Name: "c", Version: "1"}}),
			ID:             jsonrpc.NewRequestID(1),
		}
		resp, _ := mustPostMCP(t, srv, "Bearer test-token", "", initReq)
		sessID := resp.Header.Get("mcp-session-id")
		resp.Body.Close()

		// List tools
		listReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.ToolsListMethod),
			Params:         mustJSON(mcp.ListToolsRequest{}),
			ID:             jsonrpc.NewRequestID(2),
		}
		resp2, evt := mustPostMCP(t, srv, "Bearer test-token", sessID, listReq)
		defer resp2.Body.Close()

		if want, got := http.StatusOK, resp2.StatusCode; want != got {
			t.Fatalf("unexpected status: want %d got %d", want, got)
		}
		var rpcRes jsonrpc.Response
		mustUnmarshalJSON(t, evt.data, &rpcRes)
		if rpcRes.Error != nil {
			t.Fatalf("tools/list error: %+v", rpcRes.Error)
		}
		var listRes mcp.ListToolsResult
		mustUnmarshalJSON(t, rpcRes.Result, &listRes)
		if want, got := 0, len(listRes.Tools); want != got {
			t.Fatalf("unexpected tools length: want %d got %d", want, got)
		}
	})

	t.Run("Basic auth with invalid token", func(t *testing.T) {
		auth := &noAuth{wantToken: "want-token"}
		server := mcpservice.NewServer(
			mcpservice.WithToolsOptions(
				mcpservice.WithStaticToolsContainer(mcpservice.NewStaticTools()),
			),
		)
		srv := mustServer(t, server, withAuth(auth))
		defer srv.Close()

		// Attempt initialize with wrong token
		initReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.InitializeMethod),
			Params:         mustJSON(mcp.InitializeRequest{ProtocolVersion: "2025-06-18", ClientInfo: mcp.ImplementationInfo{Name: "c", Version: "1"}}),
			ID:             jsonrpc.NewRequestID("x"),
		}
		resp, _ := doPostMCP(t, srv, "Bearer bad-token", "", initReq)
		defer resp.Body.Close()
		if want, got := http.StatusUnauthorized, resp.StatusCode; want != got {
			t.Fatalf("unexpected status: want %d got %d", want, got)
		}
		// Should include a WWW-Authenticate header per spec
		if h := resp.Header.Get("www-authenticate"); !strings.HasPrefix(strings.ToLower(h), "bearer ") {
			t.Fatalf("expected WWW-Authenticate header, got %q", h)
		}
	})

	t.Run("Logging setLevel over POST", func(t *testing.T) {
		var lv slog.LevelVar
		lv.Set(slog.LevelInfo)

		server := mcpservice.NewServer(
			mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "http-logging", Version: "0.1.0"}),
			mcpservice.WithLoggingCapability(mcpservice.NewSlogLevelVarLogging(&lv)),
		)
		srv := mustServer(t, server)
		defer srv.Close()

		// Initialize to create session
		initReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.InitializeMethod),
			ID:             jsonrpc.NewRequestID(1),
			Params:         mustJSON(map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "c", "version": "0"}}),
		}
		resp, _ := mustPostMCP(t, srv, "Bearer test-token", "", initReq)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("init status: %d", resp.StatusCode)
		}
		// capture session id
		sessID := resp.Header.Get("MCP-Session-ID")
		if sessID == "" {
			t.Fatalf("missing session id")
		}

		// Now send logging/setLevel to debug
		setReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.LoggingSetLevelMethod),
			ID:             jsonrpc.NewRequestID(2),
			Params:         mustJSON(map[string]any{"level": string(mcp.LoggingLevelDebug)}),
		}
		resp2, _ := mustPostMCP(t, srv, "Bearer test-token", sessID, setReq)
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("setLevel status: %d", resp2.StatusCode)
		}
		if got := lv.Level(); got != slog.LevelDebug {
			t.Fatalf("expected debug, got %v", got)
		}
	})

	t.Run("Logging setLevel invalid level returns -32602", func(t *testing.T) {
		var lv slog.LevelVar
		lv.Set(slog.LevelInfo)

		server := mcpservice.NewServer(
			mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "http-logging", Version: "0.1.0"}),
			mcpservice.WithLoggingCapability(mcpservice.NewSlogLevelVarLogging(&lv)),
		)
		srv := mustServer(t, server)
		defer srv.Close()

		// Initialize
		initReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.InitializeMethod),
			ID:             jsonrpc.NewRequestID(1),
			Params:         mustJSON(map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "c", "version": "0"}}),
		}
		resp, _ := mustPostMCP(t, srv, "Bearer test-token", "", initReq)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("init status: %d", resp.StatusCode)
		}
		sessID := resp.Header.Get("MCP-Session-ID")
		resp.Body.Close()

		// Invalid level
		setReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.LoggingSetLevelMethod),
			ID:             jsonrpc.NewRequestID(2),
			Params:         mustJSON(map[string]any{"level": "verbose"}),
		}
		resp2, evt := mustPostMCP(t, srv, "Bearer test-token", sessID, setReq)
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("setLevel status: %d", resp2.StatusCode)
		}
		var rpcRes jsonrpc.Response
		mustUnmarshalJSON(t, evt.data, &rpcRes)
		if rpcRes.Error == nil || rpcRes.Error.Code != jsonrpc.ErrorCodeInvalidParams {
			t.Fatalf("expected invalid params error, got %#v", rpcRes.Error)
		}
	})
}

func TestMultiInstance(t *testing.T) {

	t.Run("Invalid router index", func(t *testing.T) {
		// Create server with a router that returns invalid indices
		srv := mustMultiInstanceServer(t, 2,
			func(r *http.Request, handlerCount int) int {
				return 5 // Out of bounds
			},
			func() mcpservice.ServerCapabilities { return mcpservice.NewServer() },
			withServerName("invalid-router-test"),
		)
		defer srv.Close()

		// Make a direct HTTP request since the SDK client won't work
		// when the server returns 404
		resp, err := http.Get(srv.URL + "/")
		if err != nil {
			t.Fatalf("HTTP request failed: %v", err)
		}
		defer resp.Body.Close()

		if want, got := http.StatusNotFound, resp.StatusCode; want != got {
			t.Errorf("Expected HTTP status %d, got %d", want, got)
		}
	})

	t.Run("Resources list_changed notification delivered when GET and POST hit different instances", func(t *testing.T) {
		// Shared in-memory session host to simulate distributed coordination
		sharedHost := memoryhost.New()

		// Distinct server instances per handler: each gets its own static resources container
		mcpFactory := func() mcpservice.ServerCapabilities {
			sr := mcpservice.NewStaticResources(nil, nil, nil)
			return mcpservice.NewServer(
				mcpservice.WithResourcesOptions(
					mcpservice.WithStaticResourceContainer(sr),
				),
			)
		}

		// Route GET to handler 0 and POST to handler 1
		router := func(r *http.Request, handlerCount int) int {
			if r.Method == http.MethodGet {
				return 0
			}
			if r.Method == http.MethodPost {
				return 1
			}
			return 0
		}

		srv := mustMultiInstanceServer(t, 2, router,
			mcpFactory,
			withServerName("multi-list-changed"),
			withSessionHost(sharedHost),
		)
		defer srv.Close()

		// Step 1: Initialize via POST (instance 1)
		initReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.InitializeMethod),
			Params:         mustJSON(mcp.InitializeRequest{ProtocolVersion: "2025-06-18", ClientInfo: mcp.ImplementationInfo{Name: "c", Version: "1"}}),
			ID:             jsonrpc.NewRequestID("init"),
		}
		resp, _ := mustPostMCP(t, srv, "Bearer test-token", "", initReq)
		sessID := resp.Header.Get("mcp-session-id")
		resp.Body.Close()
		if sessID == "" {
			t.Fatalf("missing session id")
		}

		// Step 2: Start GET stream on instance 0 and read next SSE event asynchronously
		respGet, eventsCh := startGetStreamOneEvent(t, srv, "Bearer test-token", sessID)
		defer respGet.Body.Close()

		// Step 3: Coordinate on server-side readiness, then publish once
		ctx := t.Context()
		readyCh := make(chan struct{}, 1)
		if err := sharedHost.SubscribeEvents(ctx, "streaminghttp/ready", func(context.Context, []byte) error {
			select {
			case readyCh <- struct{}{}:
			default:
			}
			return nil
		}); err != nil {
			t.Fatalf("subscribe ready: %v", err)
		}
		select {
		case <-readyCh:
			// stream ready
		case <-time.After(3 * time.Second):
			// proceed anyway; worst case the publish below races (rare on CI), the SSE reader still loops
		}
		_ = sharedHost.PublishEvent(ctx, string(mcp.ResourcesListChangedNotificationMethod), nil)

		// Step 4: Wait for notification
		select {
		case evt := <-eventsCh:
			var msg jsonrpc.AnyMessage
			mustUnmarshalJSON(t, evt.data, &msg)
			if msg.Method != string(mcp.ResourcesListChangedNotificationMethod) {
				t.Fatalf("unexpected method: want %q got %q", mcp.ResourcesListChangedNotificationMethod, msg.Method)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for list_changed notification")
		}
	})

	t.Run("Tools list_changed notification delivered when GET and POST hit different instances", func(t *testing.T) {
		// Shared in-memory session host to simulate distributed coordination
		sharedHost := memoryhost.New()

		// Distinct server instances per handler: each gets its own static tools container
		mcpFactory := func() mcpservice.ServerCapabilities {
			st := mcpservice.NewStaticTools()
			return mcpservice.NewServer(
				mcpservice.WithToolsOptions(
					mcpservice.WithStaticToolsContainer(st),
				),
			)
		}

		// Route GET to handler 0 and POST to handler 1
		router := func(r *http.Request, handlerCount int) int {
			if r.Method == http.MethodGet {
				return 0
			}
			if r.Method == http.MethodPost {
				return 1
			}
			return 0
		}

		srv := mustMultiInstanceServer(t, 2, router,
			mcpFactory,
			withServerName("multi-tools-list-changed"),
			withSessionHost(sharedHost),
		)
		defer srv.Close()

		// Step 1: Initialize via POST (instance 1)
		initReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.InitializeMethod),
			Params:         mustJSON(mcp.InitializeRequest{ProtocolVersion: "2025-06-18", ClientInfo: mcp.ImplementationInfo{Name: "c", Version: "1"}}),
			ID:             jsonrpc.NewRequestID("init"),
		}
		resp, _ := mustPostMCP(t, srv, "Bearer test-token", "", initReq)
		sessID := resp.Header.Get("mcp-session-id")
		resp.Body.Close()
		if sessID == "" {
			t.Fatalf("missing session id")
		}

		// Step 2: Start GET stream on instance 0 and read next SSE event asynchronously
		respGet, eventsCh := startGetStreamOneEvent(t, srv, "Bearer test-token", sessID)
		defer respGet.Body.Close()

		// Step 3: Coordinate on server-side readiness, then publish once
		ctx := t.Context()
		readyCh := make(chan struct{}, 1)
		err := sharedHost.SubscribeEvents(ctx, "streaminghttp/ready", func(context.Context, []byte) error {
			select {
			case readyCh <- struct{}{}:
			default:
			}
			return nil
		})
		if err != nil {
			t.Fatalf("subscribe ready: %v", err)
		}
		select {
		case <-readyCh:
			// stream ready
		case <-time.After(3 * time.Second):
			// proceed anyway
		}
		_ = sharedHost.PublishEvent(ctx, string(mcp.ToolsListChangedNotificationMethod), nil)

		// Step 4: Wait for notification
		select {
		case evt := <-eventsCh:
			var msg jsonrpc.AnyMessage
			mustUnmarshalJSON(t, evt.data, &msg)
			if msg.Method != string(mcp.ToolsListChangedNotificationMethod) {
				t.Fatalf("unexpected method: want %q got %q", mcp.ToolsListChangedNotificationMethod, msg.Method)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for tools list_changed notification")
		}
	})

	t.Run("Prompts list_changed notification delivered when GET and POST hit different instances", func(t *testing.T) {
		sharedHost := memoryhost.New()
		mcpFactory := func() mcpservice.ServerCapabilities {
			sp := mcpservice.NewStaticPrompts()
			return mcpservice.NewServer(
				mcpservice.WithPromptsOptions(
					mcpservice.WithStaticPromptsContainer(sp),
				),
			)
		}
		router := func(r *http.Request, handlerCount int) int {
			if r.Method == http.MethodGet {
				return 0
			}
			if r.Method == http.MethodPost {
				return 1
			}
			return 0
		}
		srv := mustMultiInstanceServer(t, 2, router,
			mcpFactory,
			withServerName("multi-prompts-list-changed"),
			withSessionHost(sharedHost),
		)
		defer srv.Close()

		initReq := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.InitializeMethod),
			Params:         mustJSON(mcp.InitializeRequest{ProtocolVersion: "2025-06-18", ClientInfo: mcp.ImplementationInfo{Name: "c", Version: "1"}}),
			ID:             jsonrpc.NewRequestID("init"),
		}
		resp, _ := mustPostMCP(t, srv, "Bearer test-token", "", initReq)
		sessID := resp.Header.Get("mcp-session-id")
		resp.Body.Close()
		if sessID == "" {
			t.Fatalf("missing session id")
		}

		respGet, eventsCh := startGetStreamOneEvent(t, srv, "Bearer test-token", sessID)
		defer respGet.Body.Close()

		// Coordinate on server readiness before publishing
		ctx := t.Context()
		readyCh := make(chan struct{}, 1)
		err := sharedHost.SubscribeEvents(ctx, "streaminghttp/ready", func(context.Context, []byte) error {
			select {
			case readyCh <- struct{}{}:
			default:
			}
			return nil
		})
		if err != nil {
			t.Fatalf("subscribe ready: %v", err)
		}
		select {
		case <-readyCh:
		case <-time.After(3 * time.Second):
		}
		_ = sharedHost.PublishEvent(ctx, string(mcp.PromptsListChangedNotificationMethod), nil)

		select {
		case evt := <-eventsCh:
			var msg jsonrpc.AnyMessage
			mustUnmarshalJSON(t, evt.data, &msg)
			if msg.Method != string(mcp.PromptsListChangedNotificationMethod) {
				t.Fatalf("unexpected method: want %q got %q", mcp.PromptsListChangedNotificationMethod, msg.Method)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for prompts list_changed notification")
		}
	})

}

// --- Direct writer (Option B) test ---
// Verifies that a server->client request emitted via a client capability while handling
// a POST request with an ID is streamed on the same SSE response even when no GET stream
// is active. We simulate this by implementing a custom ToolsCapability whose ListTools
// triggers a client roots/list call if the client advertised roots capability.
func TestPOST_Request_ServersideTriggersClientCall_StreamedInline(t *testing.T) {

	host := memoryhost.New()
	server := &testServer{}
	h, err := streaminghttp.New(context.Background(), "http://example.com/mcp", host, server, &noAuth{wantToken: "test-token"}, streaminghttp.WithServerName("t"), streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{Issuer: "https://issuer", JwksURI: "https://issuer/jwks"}))
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Correctly target /mcp path (public endpoint includes /mcp).
	initReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.InitializeMethod), ID: jsonrpc.NewRequestID("init"), Params: mustJSON(map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{"roots": map[string]any{}},
		"clientInfo":      map[string]any{"name": "c", "version": "1"},
	})}
	initResp, _ := mustPostMCPPath(t, srv, "/mcp", "Bearer test-token", "", initReq)
	sessID := initResp.Header.Get("mcp-session-id")
	if sessID == "" {
		// Fallback check with canonical header name just in case.
		sessID = initResp.Header.Get("Mcp-Session-Id")
	}
	if sessID == "" {
		b, _ := io.ReadAll(initResp.Body)
		initResp.Body.Close()
		// Provide body for debugging.
		t.Fatalf("missing session id; status=%d body=%s", initResp.StatusCode, string(b))
	}
	initResp.Body.Close()

	// Send tools/list which will internally trigger a roots/list client call, using /mcp.
	listReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ToolsListMethod), ID: jsonrpc.NewRequestID("1"), Params: mustJSON(mcp.ListToolsRequest{})}
	resp, bodyEvt := mustPostMCPPath(t, srv, "/mcp", "Bearer test-token", sessID, listReq)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}

	var first jsonrpc.Request
	if err := json.Unmarshal(bodyEvt.data, &first); err != nil {
		resp.Body.Close()
		t.Fatalf("decode first event: %v", err)
	}
	if first.Method != string(mcp.RootsListMethod) {
		resp.Body.Close()
		t.Fatalf("expected first method roots/list, got %s", first.Method)
	}
	rid := first.ID.String()
	if rid == "" {
		resp.Body.Close()
		t.Fatalf("expected non-empty outbound request id")
	}

	// Respond as client.
	rootsResp := &jsonrpc.Response{JSONRPCVersion: jsonrpc.ProtocolVersion, ID: first.ID, Result: mustJSON(mcp.ListRootsResult{Roots: []mcp.Root{}})}
	buf, _ := json.Marshal(rootsResp)
	httpReq, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("new post resp req: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer test-token")
	httpReq.Header.Set("MCP-Session-Id", sessID)
	httpReq.Header.Set("MCP-Protocol-Version", "2025-06-18")
	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("post response: %v", err)
	}
	if httpResp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		t.Fatalf("expected 202 for response POST, got %d body=%s", httpResp.StatusCode, string(b))
	}
	httpResp.Body.Close()

	second, err := readOneSSE(resp.Body)
	if err != nil {
		resp.Body.Close()
		t.Fatalf("reading second event: %v", err)
	}
	var rpcRes jsonrpc.Response
	if err := json.Unmarshal(second.data, &rpcRes); err != nil {
		resp.Body.Close()
		t.Fatalf("decode second event: %v", err)
	}
	if rpcRes.Error != nil {
		resp.Body.Close()
		t.Fatalf("unexpected error in final response: %+v", rpcRes.Error)
	}
	if rpcRes.ID.String() != "1" {
		resp.Body.Close()
		t.Fatalf("expected final response id 1, got %s", rpcRes.ID.String())
	}
	if rid == rpcRes.ID.String() {
		resp.Body.Close()
		t.Fatalf("expected different ids for outbound client call and server response")
	}
}

// ============================================================================

// logBridge is an implementation of slog.Handler that works
// with the stdlib testing pkg.
type logBridge struct {
	slog.Handler
	t   testing.TB
	buf *bytes.Buffer
	mu  *sync.Mutex
}

// Handle implements slog.Handler.
func (b *logBridge) Handle(ctx context.Context, rec slog.Record) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	err := b.Handler.Handle(ctx, rec)
	if err != nil {
		return err
	}

	output, err := io.ReadAll(b.buf)
	if err != nil {
		return err
	}

	// The output comes back with a newline, which we need to
	// trim before feeding to t.Log.
	output = bytes.TrimSuffix(output, []byte("\n"))

	// Add calldepth. But it won't be enough, and the internal slog
	// callsite will be printed. See discussion in README.md.
	b.t.Helper()

	b.t.Log(string(output))

	return nil
}

// WithAttrs implements slog.Handler.
func (b *logBridge) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &logBridge{
		t:       b.t,
		buf:     b.buf,
		mu:      b.mu,
		Handler: b.Handler.WithAttrs(attrs),
	}
}

// WithGroup implements slog.Handler.
func (b *logBridge) WithGroup(name string) slog.Handler {
	return &logBridge{
		t:       b.t,
		buf:     b.buf,
		mu:      b.mu,
		Handler: b.Handler.WithGroup(name),
	}
}

func testLogHandler(t *testing.T) *logBridge {
	b := &logBridge{
		t:   t,
		buf: &bytes.Buffer{},
		mu:  &sync.Mutex{},
	}
	hOpts := &slog.HandlerOptions{
		AddSource: false,
		Level:     slog.LevelDebug,
	}
	// The opts may have already set the handler.
	b.Handler = slog.NewTextHandler(b.buf, hOpts)

	return b
}

// ============================================================================
// Test Server Utility
// ============================================================================

type serverOption func(*serverConfig)

type serverConfig struct {
	authenticator auth.Authenticator
	mcp           mcpservice.ServerCapabilities
	sessionsHost  sessions.SessionHost
	logger        *slog.Logger
	serverName    string
	issuer        string
	jwksURI       string
}

// withAuth configures the server to use the provided authenticator.
func withAuth(authenticator auth.Authenticator) serverOption {
	return func(cfg *serverConfig) {
		cfg.authenticator = authenticator
	}
}

// withSessionHost configures the server to use the provided session host.
func withSessionHost(h sessions.SessionHost) serverOption {
	return func(cfg *serverConfig) {
		cfg.sessionsHost = h
	}
}

// withLogger configures the server to use the provided log Logger.
func withLogger(log *slog.Logger) serverOption {
	return func(cfg *serverConfig) {
		cfg.logger = log
	}
}

// withServerName configures the server name (defaults to "test-server").
func withServerName(name string) serverOption {
	return func(cfg *serverConfig) {
		cfg.serverName = name
	}
}

// withIssuer configures the OAuth issuer URL (defaults to "http://127.0.0.1:0").
func withIssuer(issuer string) serverOption {
	return func(cfg *serverConfig) {
		cfg.issuer = issuer
	}
}

// withJwksURI configures the JWKS URI (defaults to "http://127.0.0.1/.well-known/jwks.json").
func withJwksURI(uri string) serverOption {
	return func(cfg *serverConfig) {
		cfg.jwksURI = uri
	}
}

// mustServer creates a test HTTP server with the given configuration options.
// It panics on any error, making it suitable for use in tests where failures
// should immediately fail the test.
//
// The returned server includes both the httptest.Server and the configured handler.
// The caller is responsible for calling srv.Close() to clean up resources.
//
// Intelligent defaults are provided for all components:
//   - Auth: authtest.NewNoAuth("") - no authentication required
//   - Hooks: hookstest.NewMockHooks() - mock hooks with default capabilities
//   - Sessions: memory.NewStore() - in-memory session store
//   - Logger: slog.New(testLogHandler(t)) - test-friendly logging
//   - ServerName: "test-server"
//   - Issuer: "http://127.0.0.1:0"
//   - JwksURI: "http://127.0.0.1/.well-known/jwks.json"
//
// Example usage:
//
//	// Use all defaults
//	srv := mustServer(t)
//	defer srv.Close()
//
//	// Override specific components
//	srv := mustServer(t,
//		withHooks(customHooks),
//		withSessions(customStore),
//	)
//	defer srv.Close()
func mustServer(t *testing.T, mcp mcpservice.ServerCapabilities, options ...serverOption) *httptest.Server {
	ctx := context.Background()

	// Apply default configuration
	cfg := &serverConfig{
		authenticator: new(noAuth),
		mcp:           mcp,
		sessionsHost:  memoryhost.New(),
		logger:        slog.New(testLogHandler(t)),
		serverName:    "test-server",
		issuer:        "http://127.0.0.1:0",
		jwksURI:       "http://127.0.0.1/.well-known/jwks.json",
	}

	// Apply provided options
	for _, opt := range options {
		opt(cfg)
	}

	var handler http.Handler

	if cfg.mcp == nil {
		t.Fatalf("server capabilities are required")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))

	// Create the streaming HTTP handler with the test server URL
	streamingHandler, err := streaminghttp.New(
		ctx,
		srv.URL,
		cfg.sessionsHost,
		cfg.mcp,
		cfg.authenticator,
		streaminghttp.WithServerName(cfg.serverName),
		streaminghttp.WithLogger(cfg.logger),
		streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{Issuer: cfg.issuer, JwksURI: cfg.jwksURI}),
	)
	if err != nil {
		srv.Close()
		t.Fatalf("Failed to create streaming HTTP handler: %v", err)
	}

	handler = streamingHandler

	return srv
}

// RouterFunc is a function that selects which handler instance should handle a request.
// It receives the incoming request and should return the index of the handler to use.
// If the returned index is out of bounds, the request will result in a 404.
type RouterFunc func(r *http.Request, handlerCount int) int

// mustMultiInstanceServer creates a test HTTP server with multiple StreamingHTTPHandler instances
// and a configurable routing function to direct requests between them.
// This is useful for testing distributed systems scenarios where different requests
// need to be handled by different server instances.
//
// Example usage:
//
//	// Create 3 handlers with round-robin routing
//	var counter int32
//	srv := mustMultiInstanceServer(t, 3,
//		func(r *http.Request, handlerCount int) int {
//			return int(atomic.AddInt32(&counter, 1) - 1) % handlerCount
//		},
//		withServerName("multi-test-server"),
//	)
//	defer srv.Close()
func mustMultiInstanceServer(t *testing.T, handlerCount int, router RouterFunc, mcpFactory func() mcpservice.ServerCapabilities, options ...serverOption) *httptest.Server {
	if handlerCount <= 0 {
		t.Fatalf("Handler count must be positive, got %d", handlerCount)
	}
	if router == nil {
		t.Fatalf("Router function cannot be nil")
	}

	ctx := t.Context()

	// Create all handler instances
	handlers := make([]*streaminghttp.StreamingHTTPHandler, handlerCount)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := router(r, handlerCount)
		if idx < 0 || idx >= handlerCount {
			http.NotFound(w, r)
			return
		}

		handlers[idx].ServeHTTP(w, r)
	}))

	for i := 0; i < handlerCount; i++ {
		// Apply default configuration
		cfg := &serverConfig{
			authenticator: new(noAuth),
			mcp:           mcpFactory(),
			sessionsHost:  memoryhost.New(),
			logger:        slog.New(testLogHandler(t)),
			serverName:    "multi-test-server",
			issuer:        "http://127.0.0.1:0",
			jwksURI:       "http://127.0.0.1/.well-known/jwks.json",
		}

		// Apply configuration options
		for _, opt := range options {
			opt(cfg)
		}

		if cfg.mcp == nil {
			t.Fatalf("server capabilities are required")
		}

		// Each instance gets the same configuration
		streamingHandler, err := streaminghttp.New(
			ctx,
			srv.URL,
			cfg.sessionsHost,
			cfg.mcp,
			cfg.authenticator,
			streaminghttp.WithServerName(cfg.serverName),
			streaminghttp.WithLogger(cfg.logger),
			streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{Issuer: cfg.issuer, JwksURI: cfg.jwksURI}),
		)
		if err != nil {
			srv.Close()
			t.Fatalf("Failed to create streaming HTTP handler for instance %d: %v", i, err)
		}

		handlers[i] = streamingHandler
	}

	return srv
}

// ============================================================================
// Minimal HTTP/SSE client helpers (no SDK)
// ============================================================================

type sseEvent struct {
	event string
	id    string
	data  json.RawMessage
}

// doPostMCP performs the HTTP POST with required headers and returns the raw response.
func doPostMCP(t *testing.T, srv *httptest.Server, authHeader, sessionID string, req *jsonrpc.Request) (*http.Response, error) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		httpReq.Header.Set("Authorization", authHeader)
	}
	if sessionID != "" {
		httpReq.Header.Set("mcp-session-id", sessionID)
		// All non-initialize requests must include protocol version header now.
		if req.Method != string(mcp.InitializeMethod) {
			httpReq.Header.Set("MCP-Protocol-Version", "2025-06-18")
		}
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// mustPostMCP posts and parses a response. If the response is an SSE stream (text/event-stream)
// it reads exactly one event. Otherwise it reads the full body as a single JSON payload.
func mustPostMCP(t *testing.T, srv *httptest.Server, authHeader, sessionID string, req *jsonrpc.Request) (*http.Response, sseEvent) {
	t.Helper()
	resp, err := doPostMCP(t, srv, authHeader, sessionID, req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp, sseEvent{}
	}
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
		evt, err := readOneSSE(resp.Body)
		if err != nil {
			return resp, sseEvent{data: mustJSON(map[string]any{"error": fmt.Sprintf("sse read error: %v", err)})}
		}
		return resp, evt
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, sseEvent{data: mustJSON(map[string]any{"error": fmt.Sprintf("body read error: %v", err)})}
	}
	return resp, sseEvent{data: body}
}

// Added helper that allows specifying a custom path (e.g. /mcp) for POSTing MCP messages.
func mustPostMCPPath(t *testing.T, srv *httptest.Server, path string, authHeader, sessionID string, req *jsonrpc.Request) (*http.Response, sseEvent) {
	// Reuse existing logic from mustPostMCP with path override
	resp, err := doPostMCPPath(t, srv, path, authHeader, sessionID, req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp, sseEvent{}
	}
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
		evt, err := readOneSSE(resp.Body)
		if err != nil {
			return resp, sseEvent{data: mustJSON(map[string]any{"error": fmt.Sprintf("sse read error: %v", err)})}
		}
		return resp, evt
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, sseEvent{data: mustJSON(map[string]any{"error": fmt.Sprintf("body read error: %v", err)})}
	}
	return resp, sseEvent{data: body}
}

func doPostMCPPath(t *testing.T, srv *httptest.Server, path string, authHeader, sessionID string, req *jsonrpc.Request) (*http.Response, error) {
	// Mirror doPostMCP but allow custom path
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	url := srv.URL + path
	if !strings.HasPrefix(path, "/") {
		url = srv.URL + "/" + path
	}
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		httpReq.Header.Set("Authorization", authHeader)
	}
	if sessionID != "" {
		httpReq.Header.Set("mcp-session-id", sessionID)
		if req.Method != string(mcp.InitializeMethod) {
			httpReq.Header.Set("MCP-Protocol-Version", "2025-06-18")
		}
	}
	return http.DefaultClient.Do(httpReq)
}

func readOneSSE(r io.Reader) (sseEvent, error) {
	br := bufio.NewReader(r)
	var (
		event   sseEvent
		dataBuf bytes.Buffer
	)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return sseEvent{}, io.ErrUnexpectedEOF
			}
			return sseEvent{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" { // end of event
			if dataBuf.Len() > 0 {
				event.data = append([]byte(nil), dataBuf.Bytes()...)
			}
			return event, nil
		}
		if strings.HasPrefix(line, "event: ") {
			event.event = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "id: ") {
			event.id = strings.TrimPrefix(line, "id: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			if dataBuf.Len() > 0 { // support multi-line data although we emit single line
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
			continue
		}
		// ignore other fields and continue
	}
}

func mustUnmarshalJSON[T any](t *testing.T, data []byte, v *T) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal json: %v\ninput: %s", err, string(data))
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// startGetStreamOneEvent starts a GET /mcp stream and returns the response plus a channel that yields one SSE event.
func startGetStreamOneEvent(t *testing.T, srv *httptest.Server, authHeader, sessionID string) (*http.Response, <-chan sseEvent) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	if err != nil {
		t.Fatalf("new get req: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	req.Header.Set("mcp-session-id", sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do get: %v", err)
	}
	//nolint:staticcheck // SA9003 false positive
	if resp.StatusCode != http.StatusOK {
		// The stream writes headers implicitly on first write, but if nothing is written yet
		// http may report StatusOK only after first write. We'll still proceed to read.
	}
	ch := make(chan sseEvent, 1)
	go func() {
		defer close(ch)
		evt, err := readOneSSE(resp.Body)
		if err != nil {
			// signal error by sending an empty event with data set to error json
			ch <- sseEvent{event: "", data: mustJSON(map[string]any{"error": err.Error()})}
			return
		}
		ch <- evt
	}()
	return resp, ch
}

type noAuth struct {
	wantToken string
}

func (a *noAuth) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
	if a.wantToken != "" && tok != a.wantToken {
		return nil, auth.ErrUnauthorized
	}
	return &fakeUserInfo{}, nil
}

type fakeUserInfo struct{}

func (u *fakeUserInfo) UserID() string       { return "fake-user" }
func (u *fakeUserInfo) Claims(ref any) error { return nil }

// Keep optional serverOption helpers considered used to satisfy linters when not
// consumed by specific tests. These are part of the test harness API surface.
var (
	_ = withSessionHost
	_ = withLogger
	_ = withIssuer
	_ = withJwksURI
)

// ----------------------------------------------------------------------------
// Well-known endpoints
// ----------------------------------------------------------------------------

func TestAuthorizationServerMetadataMirror_ManualMode(t *testing.T) {
	server := mcpservice.NewServer(
		mcpservice.WithToolsOptions(
			mcpservice.WithStaticToolsContainer(mcpservice.NewStaticTools()),
		),
	)
	// Use explicit values so we can assert
	issuer := "http://127.0.0.1:0"
	jwks := "http://127.0.0.1/.well-known/jwks.json"
	srv := mustServer(t, server, withIssuer(issuer), withJwksURI(jwks))
	defer srv.Close()

	// Request the mirror endpoint on the RS origin
	resp, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatalf("GET metadata: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var meta struct {
		Issuer                 string   `json:"issuer"`
		ResponseTypesSupported []string `json:"response_types_supported"`
		JwksURI                string   `json:"jwks_uri"`
		ScopesSupported        []string `json:"scopes_supported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if meta.Issuer != issuer {
		t.Fatalf("issuer mismatch: want %q got %q", issuer, meta.Issuer)
	}
	if meta.JwksURI != jwks {
		t.Fatalf("jwks mismatch: want %q got %q", jwks, meta.JwksURI)
	}
	// We synthesize ["code"] in manual mode
	if len(meta.ResponseTypesSupported) == 0 || meta.ResponseTypesSupported[0] != "code" {
		t.Fatalf("unexpected response_types_supported: %#v", meta.ResponseTypesSupported)
	}
}

func TestAuthorizationServerMetadataMirror_CORS(t *testing.T) {
	server := mcpservice.NewServer(
		mcpservice.WithToolsOptions(
			mcpservice.WithStaticToolsContainer(mcpservice.NewStaticTools()),
		),
	)
	srv := mustServer(t, server)
	defer srv.Close()

	// Preflight
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/.well-known/oauth-authorization-server", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preflight request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected OPTIONS status: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("missing or wrong ACAO on OPTIONS: %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatalf("missing ACAM on OPTIONS")
	}
	resp.Body.Close()

	// GET with Origin
	getReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/.well-known/oauth-authorization-server", nil)
	getReq.Header.Set("Origin", "https://example.com")
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET request failed: %v", err)
	}
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected GET status: %d", getResp.StatusCode)
	}
	if got := getResp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("missing or wrong ACAO on GET: %q", got)
	}
	getResp.Body.Close()
}

func TestProtectedResourceMetadata_CORS(t *testing.T) {
	server := mcpservice.NewServer(
		mcpservice.WithToolsOptions(
			mcpservice.WithStaticToolsContainer(mcpservice.NewStaticTools()),
		),
	)
	srv := mustServer(t, server)
	defer srv.Close()

	// Preflight for PRM endpoint
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/.well-known/oauth-protected-resource/", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preflight PRM failed: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected OPTIONS status: %d", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing ACAO on PRM OPTIONS")
	}
	resp.Body.Close()

	// GET PRM
	getReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/.well-known/oauth-protected-resource/", nil)
	getReq.Header.Set("Origin", "https://example.com")
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET PRM failed: %v", err)
	}
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected GET status: %d", getResp.StatusCode)
	}
	if getResp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing ACAO on PRM GET")
	}
	getResp.Body.Close()
}
