package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/examples/echo"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
	"github.com/ggoodman/mcp-server-go/streaminghttp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type noAuth struct{}

func (a *noAuth) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
	return fakeUserInfo("user-1"), nil
}

type fakeUserInfo string

func (u fakeUserInfo) UserID() string       { return string(u) }
func (u fakeUserInfo) Claims(ref any) error { return nil }

// authRT injects an Authorization header for test requests.
type authRT struct{ base http.RoundTripper }

func (t authRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer test-token")
	return t.base.RoundTrip(r)
}

// TestExamples_Echo_E2E spins up the streaming HTTP handler with the echo example
// server and verifies the client can list and call the echo tool.
func TestExamples_Echo_E2E(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	// Create server with dynamic handler so we can pass the test server URL into handler config.
	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer srv.Close()

	// Create server handler with manual OIDC config (bypasses discovery)
	mh := memoryhost.New()
	h, err := streaminghttp.New(
		ctx,
		srv.URL,
		mh,
		echo.New(),
		new(noAuth),
		streaminghttp.WithServerName("examples"),
		streaminghttp.WithManualOIDC(streaminghttp.ManualOIDC{
			Issuer:  "http://127.0.0.1:0",
			JwksURI: "http://127.0.0.1/.well-known/jwks.json",
		}),
	)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}
	handler = h

	client := sdk.NewClient(&sdk.Implementation{Name: "e2e", Version: "0.0.0"}, &sdk.ClientOptions{})
	transport := &sdk.StreamableClientTransport{
		Endpoint:   srv.URL + "/",
		HTTPClient: &http.Client{Transport: authRT{base: http.DefaultTransport}},
	}
	cs, err := client.Connect(ctx, transport, &sdk.ClientSessionOptions{})
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer cs.Close()

	// List tools
	lt, err := cs.ListTools(ctx, &sdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(lt.Tools) != 1 || lt.Tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", lt.Tools)
	}

	// Call echo tool
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{
		Name: "echo",
		Arguments: map[string]any{
			"message": "hello",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("unexpected empty call result: %+v", res)
	}
}
