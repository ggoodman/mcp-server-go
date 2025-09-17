package stdio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
)

// writeMux implements jsonrpcWriter for tests via the real mux.
func newTestMux(buf *bytes.Buffer) *writeMux { return &writeMux{w: bufio.NewWriter(buf)} }

func readLines(t *testing.T, s string) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func waitForBufferLines(t *testing.T, mux *writeMux, buf *bytes.Buffer, n int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		_ = mux.w.Flush()
		lines := readLines(t, buf.String())
		if len(lines) >= n {
			return lines
		}
		if time.Now().After(deadline) {
			return lines
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOutboundDispatcher_RequestResponse_OutOfOrder(t *testing.T) {
	t.Parallel()

	var w bytes.Buffer
	mux := newTestMux(&w)
	d := newOutboundDispatcher(mux)

	ctx := context.Background()

	// Issue two requests
	resCh1 := make(chan *jsonrpc.Response, 1)
	resCh2 := make(chan *jsonrpc.Response, 1)

	go func() {
		resp, err := d.Call(ctx, "test/m1", map[string]any{"a": 1})
		if err != nil {
			t.Errorf("call1: %v", err)
			return
		}
		resCh1 <- resp
	}()
	go func() {
		resp, err := d.Call(ctx, "test/m2", map[string]any{"b": 2})
		if err != nil {
			t.Errorf("call2: %v", err)
			return
		}
		resCh2 <- resp
	}()

	// Extract written requests from buffer
	lines := waitForBufferLines(t, mux, &w, 2, time.Second)
	if len(lines) != 2 {
		t.Fatalf("expected 2 outbound requests, got %d", len(lines))
	}
	var req1, req2 jsonrpc.Request
	_ = json.Unmarshal([]byte(lines[0]), &req1)
	_ = json.Unmarshal([]byte(lines[1]), &req2)

	// Reply out of order: respond to req2 first
	resp2, _ := jsonrpc.NewResultResponse(req2.ID, map[string]any{"ok": 2})
	d.OnResponse(resp2)
	resp1, _ := jsonrpc.NewResultResponse(req1.ID, map[string]any{"ok": 1})
	d.OnResponse(resp1)

	// Validate
	got2 := <-resCh2
	got1 := <-resCh1
	if got2.Error != nil || got1.Error != nil {
		t.Fatalf("unexpected errors: %+v %+v", got2.Error, got1.Error)
	}
}

func TestOutboundDispatcher_CancelContext_SendsCancelled(t *testing.T) {
	t.Parallel()

	var w bytes.Buffer
	mux := newTestMux(&w)
	d := newOutboundDispatcher(mux)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.Call(ctx, "test/m", nil)
		done <- err
	}()

	// Find the request ID written
	lines := waitForBufferLines(t, mux, &w, 1, time.Second)
	if len(lines) < 1 {
		t.Fatalf("expected at least 1 outbound request, got %d", len(lines))
	}
	var req jsonrpc.Request
	_ = json.Unmarshal([]byte(lines[0]), &req)

	// Cancel the context
	cancel()
	err := <-done
	if err == nil {
		t.Fatalf("expected error from cancelled call")
	}

	// A notifications/cancelled should have been written
	lines = waitForBufferLines(t, mux, &w, 2, time.Second)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 outbound messages (request + cancel), got %d", len(lines))
	}
	// Last line should be cancelled notification referencing req.ID
	var last jsonrpc.AnyMessage
	_ = json.Unmarshal([]byte(lines[len(lines)-1]), &last)
	if last.Method == "" || last.Method != "notifications/cancelled" {
		t.Fatalf("expected cancelled notification, got %s", last.Method)
	}
	var p struct {
		RequestID string `json:"requestId"`
	}
	_ = json.Unmarshal(last.Params, &p)
	if p.RequestID != req.ID.String() {
		t.Fatalf("cancelled requestId mismatch: got %s want %s", p.RequestID, req.ID.String())
	}
}

func TestOutboundDispatcher_RemoteCancelled(t *testing.T) {
	t.Parallel()

	var w bytes.Buffer
	mux := newTestMux(&w)
	d := newOutboundDispatcher(mux)

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := d.Call(ctx, "test/m", nil)
		if !errorsIs(err, ErrRemoteCancelled) {
			t.Errorf("expected ErrRemoteCancelled, got %v", err)
		}
	}()

	lines := waitForBufferLines(t, mux, &w, 1, time.Second)
	if len(lines) < 1 {
		t.Fatalf("expected at least 1 outbound request, got %d", len(lines))
	}
	var req jsonrpc.Request
	_ = json.Unmarshal([]byte(lines[0]), &req)

	// Simulate client notifications/cancelled
	notif := jsonrpc.AnyMessage{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: "notifications/cancelled", Params: mustJSON(map[string]any{"requestId": req.ID.String()})}
	d.OnNotification(notif)
	wg.Wait()
}

func errorsIs(err, target error) bool {
	return (err != nil && target != nil && err.Error() == target.Error())
}
