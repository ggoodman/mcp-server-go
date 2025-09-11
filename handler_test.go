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

	streaminghttp "github.com/ggoodman/mcp-server-go"
	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpserver"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
)

func TestSingleInstance(t *testing.T) {
	t.Run("Initialize returns session and capabilities", func(t *testing.T) {
		// Explicit minimal server with empty static tools
		server := mcpserver.NewServer(
			mcpserver.WithToolsOptions(
				mcpserver.WithStaticToolsContainer(mcpserver.NewStaticTools()),
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

	t.Run("Tools list over POST is empty", func(t *testing.T) {
		server := mcpserver.NewServer(
			mcpserver.WithToolsOptions(
				mcpserver.WithStaticToolsContainer(mcpserver.NewStaticTools()),
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
		server := mcpserver.NewServer(
			mcpserver.WithToolsOptions(
				mcpserver.WithStaticToolsContainer(mcpserver.NewStaticTools()),
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
}

func TestMultiInstance(t *testing.T) {

	t.Run("Invalid router index", func(t *testing.T) {
		// Create server with a router that returns invalid indices
		srv := mustMultiInstanceServer(t, 2,
			func(r *http.Request, handlerCount int) int {
				return 5 // Out of bounds
			},
			func() mcpserver.ServerCapabilities { return mcpserver.NewServer() },
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
		mcpFactory := func() mcpserver.ServerCapabilities {
			sr := mcpserver.NewStaticResources(nil, nil, nil)
			return mcpserver.NewServer(
				mcpserver.WithResourcesOptions(
					mcpserver.WithStaticResourceContainer(sr),
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

		// Step 3: Trigger a resources list change by publishing the internal server event.
		// This emulates any instance detecting a change and publishing to the session-scoped topic.
		ctx := t.Context()
		_ = sharedHost.PublishEvent(ctx, sessID, string(mcp.ResourcesListChangedNotificationMethod), nil)

		// Step 4: Wait for notification
		select {
		case evt := <-eventsCh:
			if evt.event != "notification" {
				t.Fatalf("expected notification event, got %q", evt.event)
			}
			var msg jsonrpc.AnyMessage
			mustUnmarshalJSON(t, evt.data, &msg)
			if msg.Method != string(mcp.ResourcesListChangedNotificationMethod) {
				t.Fatalf("unexpected method: want %q got %q", mcp.ResourcesListChangedNotificationMethod, msg.Method)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for list_changed notification")
		}
	})

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
	mcp           mcpserver.ServerCapabilities
	sessionsHost  sessions.SessionHost
	logHandler    slog.Handler
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

// withLogHandler configures the server to use the provided log handler.
func withLogHandler(handler slog.Handler) serverOption {
	return func(cfg *serverConfig) {
		cfg.logHandler = handler
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
//   - LogHandler: testLogHandler(t) - test-friendly logging
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
func mustServer(t *testing.T, mcp mcpserver.ServerCapabilities, options ...serverOption) *httptest.Server {
	ctx := context.Background()

	// Apply default configuration
	cfg := &serverConfig{
		authenticator: new(noAuth),
		mcp:           mcp,
		sessionsHost:  memoryhost.New(),
		logHandler:    testLogHandler(t),
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
		streaminghttp.WithLogger(cfg.logHandler),
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
func mustMultiInstanceServer(t *testing.T, handlerCount int, router RouterFunc, mcpFactory func() mcpserver.ServerCapabilities, options ...serverOption) *httptest.Server {
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
			logHandler:    testLogHandler(t),
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
			streaminghttp.WithLogger(cfg.logHandler),
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
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// mustPostMCP posts and parses a single SSE event from the response body.
func mustPostMCP(t *testing.T, srv *httptest.Server, authHeader, sessionID string, req *jsonrpc.Request) (*http.Response, sseEvent) {
	t.Helper()
	resp, err := doPostMCP(t, srv, authHeader, sessionID, req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	// Only 200 OK responses are expected to carry an SSE event.
	if resp.StatusCode != http.StatusOK {
		return resp, sseEvent{}
	}
	evt, err := readOneSSE(resp.Body)
	if err != nil {
		// ensure body is closed by caller even on error
		return resp, sseEvent{data: mustJSON(map[string]any{"error": fmt.Sprintf("sse read error: %v", err)})}
	}
	return resp, evt
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
			// finalize
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
			// handler writes single-line JSON via json.Encoder
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
			continue
		}
		// ignore other fields
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
	_ = withLogHandler
	_ = withIssuer
	_ = withJwksURI
)
