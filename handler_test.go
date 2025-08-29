package streaminghttp

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ggoodman/mcp-streaming-http-go/hooks"
	"github.com/ggoodman/mcp-streaming-http-go/mcp"
	"github.com/ggoodman/mcp-streaming-http-go/sessions/memory"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSingleInstance(t *testing.T) {
	t.Run("Simple tool call", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var handler http.Handler

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handler.ServeHTTP(w, r)
		}))
		defer srv.Close()

		handler, err := NewStreamingHTTPHandlerWithManualOIDC(ctx, ManualOIDCConfig{
			ServerURL:  srv.URL,
			ServerName: "TestServer",
			Issuer:     "http://127.0.0.1:0",
			JwksURI:    "http://127.0.0.1/.well-known/jwks.json",
			Hooks:      &mockHooks{},
			Sessions:   memory.NewStore(),
			LogHandler: testLogHandler(t),
		})
		if err != nil {
			t.Fatalf("Failed to create handler: %v", err)
		}

		client := sdk.NewClient(&sdk.Implementation{
			Name:    "test-client",
			Version: "1.0.0",
			Title:   "Test Client",
		}, &sdk.ClientOptions{})
		transport := &sdk.StreamableClientTransport{
			Endpoint: srv.URL + "/",
		}
		cs, err := client.Connect(ctx, transport, &sdk.ClientSessionOptions{})
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer cs.Close()

		// Match the implementation info from mockHooks
		if want, got := "test-server", cs.InitializeResult().ServerInfo.Name; want != got {
			t.Errorf("Unexpected server name: want %q, got %q", want, got)
		}

		params := &sdk.CallToolParams{
			Name:      "greet",
			Arguments: map[string]any{"name": "you"},
		}
		res, err := cs.CallTool(ctx, params)
		if err != nil {
			t.Fatalf("CallTool failed: %v", err)
		}

		if res.IsError {
			t.Fatalf("CallTool returned error: %v", res.Content)
		}
	})
}

// ============================================================================

// mockHooks provides a minimal implementation of hooks.Hooks for testing
type mockHooks struct{}

func (m *mockHooks) Initialize(ctx context.Context, session hooks.Session, req *mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	return &mcp.InitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: mcp.ServerCapabilities{
			Tools: &struct {
				ListChanged bool `json:"listChanged"`
			}{
				ListChanged: false,
			},
		},
		ServerInfo: mcp.ImplementationInfo{
			Name:    "test-server",
			Version: "1.0.0",
		},
	}, nil
}

func (m *mockHooks) GetToolsCapability() hooks.ToolsCapability             { return nil }
func (m *mockHooks) GetResourcesCapability() hooks.ResourcesCapability     { return nil }
func (m *mockHooks) GetPromptsCapability() hooks.PromptsCapability         { return nil }
func (m *mockHooks) GetLoggingCapability() hooks.LoggingCapability         { return nil }
func (m *mockHooks) GetCompletionsCapability() hooks.CompletionsCapability { return nil }

// ============================================================================

// Bridge is an implementation of slog.Handler that works
// with the stdlib testing pkg.
type Bridge struct {
	slog.Handler
	t   testing.TB
	buf *bytes.Buffer
	mu  *sync.Mutex
}

// Handle implements slog.Handler.
func (b *Bridge) Handle(ctx context.Context, rec slog.Record) error {
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
func (b *Bridge) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Bridge{
		t:       b.t,
		buf:     b.buf,
		mu:      b.mu,
		Handler: b.Handler.WithAttrs(attrs),
	}
}

// WithGroup implements slog.Handler.
func (b *Bridge) WithGroup(name string) slog.Handler {
	return &Bridge{
		t:       b.t,
		buf:     b.buf,
		mu:      b.mu,
		Handler: b.Handler.WithGroup(name),
	}
}

func testLogHandler(t *testing.T) *Bridge {
	b := &Bridge{
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
