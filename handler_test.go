package streaminghttp_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	streaminghttp "github.com/ggoodman/mcp-streaming-http-go"
	"github.com/ggoodman/mcp-streaming-http-go/auth"
	"github.com/ggoodman/mcp-streaming-http-go/auth/authtest"
	"github.com/ggoodman/mcp-streaming-http-go/hooks"
	"github.com/ggoodman/mcp-streaming-http-go/hooks/hookstest"
	"github.com/ggoodman/mcp-streaming-http-go/mcp"
	"github.com/ggoodman/mcp-streaming-http-go/sessions"
	"github.com/ggoodman/mcp-streaming-http-go/sessions/memory"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"
)

func TestSingleInstance(t *testing.T) {

	t.Run("Basic list tools", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		srv := mustServer(t,
			withHooks(hookstest.NewMockHooks(hookstest.WithEmptyToolsCapability())),
		)
		defer srv.Close()

		cs := mustClient(t, srv)
		defer cs.Close()

		// Verify we can list tools (should be empty due to empty tools capability)
		res, err := cs.ListTools(ctx, &sdk.ListToolsParams{})
		if err != nil {
			t.Fatalf("CallTool failed: %v", err)
		}

		if want, got := 0, len(res.Tools); want != got {
			t.Errorf("Unexpected number of tools: want %d, got %d", want, got)
		}

		// Attempt to call a tool that doesn't exist and make sure
		// the resulting error is a NotFoundError
		_, err = cs.CallTool(ctx, &sdk.CallToolParams{
			Name: "non-existent-tool",
		})
		if err == nil {
			t.Fatalf("Expected error calling non-existent tool, got nil")
		}

		if want, got := `calling "tools/call": tool not found: non-existent-tool`, err.Error(); want != got {
			t.Errorf("Unexpected error message: want %q, got %q", want, got)
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

	t.Run("Tool call request and cancellation routed to different instances", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		blockCh := make(chan struct{})
		defer close(blockCh)

		toolStartedCh := make(chan struct{}, 1)
		defer close(toolStartedCh)

		toolCancelledCh := make(chan struct{}, 1)
		defer close(toolCancelledCh)

		toolStartedFlag := atomic.Bool{}

		didCancel := atomic.Bool{}

		srv := mustMultiInstanceServer(t, 2,
			func(r *http.Request, handlerCount int) int {
				if toolStartedFlag.Load() {
					return 1
				}

				return 0
			},
			withLogHandler(testLogHandler(t)),
			withHooks(
				hookstest.NewMockHooks(
					hookstest.WithToolsCapability(
						hookstest.NewMockToolsCapability(
							hookstest.WithCallToolHandler(func(ctx context.Context, session hooks.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
								toolStartedFlag.Store(true)
								toolStartedCh <- struct{}{}

								select {
								case <-blockCh:
									t.Fatalf("Tool call should have been cancelled")
								case <-ctx.Done():
									didCancel.Store(true)
									toolCancelledCh <- struct{}{}
									return nil, ctx.Err()
								}

								return nil, nil
							}),
						),
					),
				),
			),
		)
		defer srv.Close()

		cs := mustClient(t, srv)
		defer cs.Close()

		g, ctx := errgroup.WithContext(ctx)

		toolCtx, toolCancel := context.WithCancel(ctx)
		defer toolCancel()

		g.Go(func() error {
			// The SDK optimistically returns an error after sending a cancellation
			// notification. This won't give us time to verify the cancellation handler
			// was invoked, so we ignore the error here and check the cancellation flag instead.
			cs.CallTool(toolCtx, &sdk.CallToolParams{
				Name:      "test-tool",
				Arguments: map[string]any{},
			})

			if want, got := true, didCancel.Load(); want != got {
				t.Errorf("Expected tool handler to be cancelled: want %v, got %v", want, got)
			}

			return nil
		})

		g.Go(func() error {
			<-toolStartedCh

			toolCancel()

			<-toolCancelledCh

			return nil
		})

		err := g.Wait()
		if err != nil {
			t.Fatalf("Tool call goroutines failed: %v", err)
		}

		if want, got := true, didCancel.Load(); want != got {
			t.Errorf("Expected tool handler to be cancelled: want %v, got %v", want, got)
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
	hooks         hooks.Hooks
	sessions      sessions.SessionManager
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

// withHooks configures the server to use the provided hooks implementation.
func withHooks(hooks hooks.Hooks) serverOption {
	return func(cfg *serverConfig) {
		cfg.hooks = hooks
	}
}

// withSessions configures the server to use the provided session store.
func withSessions(sessions sessions.SessionManager) serverOption {
	return func(cfg *serverConfig) {
		cfg.sessions = sessions
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
func mustServer(t *testing.T, options ...serverOption) *httptest.Server {
	ctx := context.Background()

	// Apply default configuration
	cfg := &serverConfig{
		authenticator: authtest.NewNoAuth(""),
		hooks:         hookstest.NewMockHooks(),
		sessions:      memory.NewStore(),
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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))

	// Create the streaming HTTP handler with the test server URL
	streamingHandler, err := streaminghttp.NewStreamingHTTPHandlerWithManualOIDC(ctx, streaminghttp.ManualOIDCConfig{
		ServerURL:     srv.URL,
		ServerName:    cfg.serverName,
		Issuer:        cfg.issuer,
		JwksURI:       cfg.jwksURI,
		Authenticator: cfg.authenticator,
		Hooks:         cfg.hooks,
		Sessions:      cfg.sessions,
		LogHandler:    cfg.logHandler,
	})
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
func mustMultiInstanceServer(t *testing.T, handlerCount int, router RouterFunc, options ...serverOption) *httptest.Server {
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
			authenticator: authtest.NewNoAuth(""),
			hooks:         hookstest.NewMockHooks(),
			sessions:      memory.NewStore(),
			logHandler:    testLogHandler(t),
			serverName:    "multi-test-server",
			issuer:        "http://127.0.0.1:0",
			jwksURI:       "http://127.0.0.1/.well-known/jwks.json",
		}

		// Apply configuration options
		for _, opt := range options {
			opt(cfg)
		}

		// Each instance gets the same configuration
		streamingHandler, err := streaminghttp.NewStreamingHTTPHandlerWithManualOIDC(ctx, streaminghttp.ManualOIDCConfig{
			ServerURL:     srv.URL,
			ServerName:    cfg.serverName,
			Issuer:        cfg.issuer,
			JwksURI:       cfg.jwksURI,
			Authenticator: cfg.authenticator,
			Hooks:         cfg.hooks,
			Sessions:      cfg.sessions,
			LogHandler:    cfg.logHandler,
		})
		if err != nil {
			srv.Close()
			t.Fatalf("Failed to create streaming HTTP handler for instance %d: %v", i, err)
		}

		handlers[i] = streamingHandler
	}

	return srv
}

// ============================================================================
// Test Client Utility
// ============================================================================

type clientOption func(*clientConfig)

type clientConfig struct {
	name    string
	version string
	title   string
}

// withClientName configures the client name (defaults to "test-client").
func withClientName(name string) clientOption {
	return func(cfg *clientConfig) {
		cfg.name = name
	}
}

// withClientVersion configures the client version (defaults to "1.0.0").
func withClientVersion(version string) clientOption {
	return func(cfg *clientConfig) {
		cfg.version = version
	}
}

// withClientTitle configures the client title (defaults to "Test Client").
func withClientTitle(title string) clientOption {
	return func(cfg *clientConfig) {
		cfg.title = title
	}
}

// mustClient creates a test MCP client connected to the given server with intelligent defaults.
// It panics on any error, making it suitable for use in tests where failures
// should immediately fail the test.
//
// The returned client session is already initialized, validated, and ready to use.
// The initialization process is verified to ensure the server responds correctly.
// The caller is responsible for calling cs.Close() to clean up resources.
//
// Intelligent defaults are provided:
//   - Name: "test-client"
//   - Version: "1.0.0"
//   - Title: "Test Client"
//
// Example usage:
//
//	// Use all defaults
//	srv := mustServer(t)
//	defer srv.Close()
//	cs := mustClient(t, srv)
//	defer cs.Close()
//
//	// Override specific client info
//	cs := mustClient(t, srv,
//		withClientName("custom-client"),
//		withClientVersion("2.0.0"),
//	)
//	defer cs.Close()
func mustClient(t *testing.T, srv *httptest.Server, options ...clientOption) *sdk.ClientSession {
	ctx := context.Background()

	// Apply default configuration
	cfg := &clientConfig{
		name:    "test-client",
		version: "1.0.0",
		title:   "Test Client",
	}

	// Apply provided options
	for _, opt := range options {
		opt(cfg)
	}

	client := sdk.NewClient(&sdk.Implementation{
		Name:    cfg.name,
		Version: cfg.version,
		Title:   cfg.title,
	}, &sdk.ClientOptions{})

	transport := &sdk.StreamableClientTransport{
		Endpoint: srv.URL + "/",
	}

	cs, err := client.Connect(ctx, transport, &sdk.ClientSessionOptions{})
	if err != nil {
		t.Fatalf("Failed to connect client: %v", err)
	}

	// Verify initialization completed successfully
	initResult := cs.InitializeResult()
	if initResult == nil {
		t.Fatalf("Client session not initialized")
	}

	// Verify server info is present
	if initResult.ServerInfo.Name == "" {
		t.Fatalf("Server info missing or invalid")
	}

	// Log the server info for debugging
	t.Logf("Connected to server: %s (protocol version: %s)",
		initResult.ServerInfo.Name,
		initResult.ProtocolVersion)

	return cs
}
