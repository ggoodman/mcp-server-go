package streaminghttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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

		handler, err := NewStreamingHTTPHandler(ctx, StreamingHTTPConfig{
			ServerURL:              srv.URL,
			ServerName:             "TestServer",
			AuthorizationServerURL: "http://127.0.0.1:0",
		})
		if err != nil {
			t.Fatalf("Failed to create handler: %v", err)
		}

	})
}
