package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	streaminghttp "github.com/ggoodman/mcp-server-go"
	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/examples/resources_static"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type noAuthRes struct{}

func (a *noAuthRes) CheckAuthentication(ctx context.Context, tok string) (auth.UserInfo, error) {
	return fakeUserInfo("user-1"), nil
}

// Reuse fakeUserInfo and authRT from examples_e2e_test.go

func TestExamples_ResourcesStatic_E2E(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer srv.Close()

	mh := memoryhost.New()
	h, err := streaminghttp.New(
		ctx,
		srv.URL,
		mh,
		resources_static.New(),
		new(noAuthRes),
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

	// List resources
	lr, err := cs.ListResources(ctx, &sdk.ListResourcesParams{})
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}
	if len(lr.Resources) == 0 {
		t.Fatalf("expected some resources; got none")
	}

	// Read resource
	_, err = cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: lr.Resources[0].URI})
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}
}
