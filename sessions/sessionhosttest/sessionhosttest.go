package sessionhosttest

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-streaming-http-go/sessions"
)

// HostFactory creates a new SessionHost instance for testing.
type HostFactory func(t *testing.T) sessions.SessionHost

// RunSessionHostTests runs the complete SessionHost test suite against the provided factory.
func RunSessionHostTests(t *testing.T, factory HostFactory) {
	t.Run("Messaging_PublishAndSubscribeFromBeginning", func(t *testing.T) {
		testPublishAndSubscribeFromBeginning(t, factory)
	})
	t.Run("Messaging_PublishAndResumeFromLastEventID", func(t *testing.T) {
		testPublishAndSubscribeFromLastEventID(t, factory)
	})
	t.Run("Messaging_IsolationBetweenSessions", func(t *testing.T) {
		testSessionIsolation(t, factory)
	})
	t.Run("Messaging_SubscriptionContextCancellation", func(t *testing.T) {
		testSubscriptionContextCancellation(t, factory)
	})
	t.Run("Messaging_HandlerErrorStopsSubscription", func(t *testing.T) {
		testHandlerErrorStopsSubscription(t, factory)
	})
	t.Run("Messaging_CleanupSession", func(t *testing.T) {
		testCleanupSession(t, factory)
	})
	t.Run("Messaging_ResumeFromNonExistentEventID", func(t *testing.T) {
		testResumeFromNonExistentEventID(t, factory)
	})

	t.Run("Revocation_PreciseRevocationIfSupported", func(t *testing.T) {
		testPreciseRevocationIfSupported(t, factory)
	})
	t.Run("Revocation_EpochBumpIfSupported", func(t *testing.T) {
		testEpochBumpIfSupported(t, factory)
	})

	t.Run("ManagerIntegration_CreateLoadDeleteWithJWS", func(t *testing.T) {
		testManagerIntegrationJWS(t, factory)
	})
}

// --- Messaging tests ---

func testPublishAndSubscribeFromBeginning(t *testing.T, factory HostFactory) {
	h := factory(t)
	defer cleanupHost(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionID := "sess-1"

	// Create a test message
	req := &jsonrpc.Request{JSONRPCVersion: "2.0", Method: "test/method", ID: jsonrpc.NewRequestID(1)}
	reqBytes, _ := json.Marshal(req)

	var received []struct {
		id   string
		data []byte
	}
	var mu sync.Mutex

	done := make(chan error, 1)
	go func() {
		err := h.SubscribeSession(ctx, sessionID, "", func(ctx context.Context, msgID string, msg []byte) error {
			mu.Lock()
			received = append(received, struct {
				id   string
				data []byte
			}{msgID, msg})
			mu.Unlock()
			cancel()
			return nil
		})
		done <- err
	}()

	time.Sleep(100 * time.Millisecond)

	evID, err := h.PublishSession(ctx, sessionID, reqBytes)
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if evID == "" {
		t.Fatalf("expected non-empty event id")
	}

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("subscribe returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe timeout")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 message, got %d", len(received))
	}
	if received[0].id != evID {
		t.Fatalf("expected event id %s, got %s", evID, received[0].id)
	}

	var got jsonrpc.Request
	if err := json.Unmarshal(received[0].data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Method != req.Method {
		t.Fatalf("expected method %s, got %s", req.Method, got.Method)
	}
}

func testPublishAndSubscribeFromLastEventID(t *testing.T, factory HostFactory) {
	h := factory(t)
	defer cleanupHost(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionID := "sess-2"

	r1 := &jsonrpc.Request{JSONRPCVersion: "2.0", Method: "test/m1", ID: jsonrpc.NewRequestID(1)}
	b1, _ := json.Marshal(r1)
	ev1, err := h.PublishSession(ctx, sessionID, b1)
	if err != nil {
		t.Fatalf("publish 1: %v", err)
	}

	r2 := &jsonrpc.Request{JSONRPCVersion: "2.0", Method: "test/m2", ID: jsonrpc.NewRequestID(2)}
	b2, _ := json.Marshal(r2)
	ev2, err := h.PublishSession(ctx, sessionID, b2)
	if err != nil {
		t.Fatalf("publish 2: %v", err)
	}

	var received []struct {
		id   string
		data []byte
	}
	var mu sync.Mutex
	done := make(chan error, 1)

	go func() {
		err := h.SubscribeSession(ctx, sessionID, ev1, func(ctx context.Context, msgID string, msg []byte) error {
			mu.Lock()
			received = append(received, struct {
				id   string
				data []byte
			}{msgID, msg})
			mu.Unlock()
			cancel()
			return nil
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("subscribe: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe timeout")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(received))
	}
	if received[0].id != ev2 {
		t.Fatalf("expected id %s, got %s", ev2, received[0].id)
	}

	var got jsonrpc.Request
	if err := json.Unmarshal(received[0].data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Method != r2.Method {
		t.Fatalf("expected %s, got %s", r2.Method, got.Method)
	}
}

func testSessionIsolation(t *testing.T, factory HostFactory) {
	h := factory(t)
	defer cleanupHost(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s1, s2 := "sess-3a", "sess-3b"

	r1 := &jsonrpc.Request{JSONRPCVersion: "2.0", Method: "test/a", ID: jsonrpc.NewRequestID(1)}
	b1, _ := json.Marshal(r1)
	r2 := &jsonrpc.Request{JSONRPCVersion: "2.0", Method: "test/b", ID: jsonrpc.NewRequestID(2)}
	b2, _ := json.Marshal(r2)

	var got1, got2 []string
	var mu1, mu2 sync.Mutex

	d1 := make(chan error, 1)
	go func() {
		err := h.SubscribeSession(ctx, s1, "", func(ctx context.Context, id string, msg []byte) error {
			var req jsonrpc.Request
			_ = json.Unmarshal(msg, &req)
			mu1.Lock()
			got1 = append(got1, req.Method)
			mu1.Unlock()
			return nil
		})
		d1 <- err
	}()

	d2 := make(chan error, 1)
	go func() {
		err := h.SubscribeSession(ctx, s2, "", func(ctx context.Context, id string, msg []byte) error {
			var req jsonrpc.Request
			_ = json.Unmarshal(msg, &req)
			mu2.Lock()
			got2 = append(got2, req.Method)
			mu2.Unlock()
			return nil
		})
		d2 <- err
	}()

	time.Sleep(100 * time.Millisecond)
	if _, err := h.PublishSession(ctx, s1, b1); err != nil {
		t.Fatalf("publish s1: %v", err)
	}
	if _, err := h.PublishSession(ctx, s2, b2); err != nil {
		t.Fatalf("publish s2: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	cancel()

	<-d1
	<-d2

	mu1.Lock()
	c1 := len(got1)
	mu1.Unlock()
	mu2.Lock()
	c2 := len(got2)
	mu2.Unlock()
	if c1 != 1 {
		t.Fatalf("s1 expected 1, got %d", c1)
	}
	if c2 != 1 {
		t.Fatalf("s2 expected 1, got %d", c2)
	}
}

func testSubscriptionContextCancellation(t *testing.T, factory HostFactory) {
	h := factory(t)
	defer cleanupHost(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	sessionID := "sess-4"
	done := make(chan error, 1)
	go func() {
		done <- h.SubscribeSession(ctx, sessionID, "", func(ctx context.Context, id string, msg []byte) error { return nil })
	}()

	select {
	case err := <-done:
		if err != context.DeadlineExceeded {
			t.Fatalf("expected deadline exceeded, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe timeout")
	}
}

func testHandlerErrorStopsSubscription(t *testing.T, factory HostFactory) {
	h := factory(t)
	defer cleanupHost(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionID := "sess-5"
	req := &jsonrpc.Request{JSONRPCVersion: "2.0", Method: "test/m", ID: jsonrpc.NewRequestID(1)}
	b, _ := json.Marshal(req)
	expected := fmt.Errorf("handler error")

	done := make(chan error, 1)
	go func() {
		done <- h.SubscribeSession(ctx, sessionID, "", func(ctx context.Context, id string, msg []byte) error { return expected })
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err := h.PublishSession(ctx, sessionID, b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, expected) {
			t.Fatalf("expected handler error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe timeout")
	}
}

func testCleanupSession(t *testing.T, factory HostFactory) {
	h := factory(t)
	defer cleanupHost(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionID := "sess-6"
	req := &jsonrpc.Request{JSONRPCVersion: "2.0", Method: "test/m", ID: jsonrpc.NewRequestID(1)}
	b, _ := json.Marshal(req)

	ev, err := h.PublishSession(ctx, sessionID, b)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := h.CleanupSession(ctx, sessionID); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	subCtx, subCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer subCancel()
	err = h.SubscribeSession(subCtx, sessionID, ev, func(ctx context.Context, id string, msg []byte) error { t.Fatal("should not receive"); return nil })
	if err != nil && err != context.DeadlineExceeded {
		t.Logf("acceptable subscribe error after cleanup: %v", err)
	}
}

func testResumeFromNonExistentEventID(t *testing.T, factory HostFactory) {
	h := factory(t)
	defer cleanupHost(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sessionID := "sess-7"
	nonExistent := "non-existent-id"
	err := h.SubscribeSession(ctx, sessionID, nonExistent, func(ctx context.Context, id string, msg []byte) error { return nil })
	// Implementations may either return an error immediately, or block until deadline with no delivery.
	if err == nil {
		t.Logf("subscribe returned nil for non-existent event id; acceptable if no messages were delivered until deadline")
	}
}

// --- Revocation tests (optional) ---

func testPreciseRevocationIfSupported(t *testing.T, factory HostFactory) {
	h := factory(t)
	defer cleanupHost(t, h)
	ctx := context.Background()

	sessionID := "sess-8"
	if err := h.AddRevocation(ctx, sessionID, 2*time.Minute); err != nil {
		if errors.Is(err, sessions.ErrRevocationUnsupported) {
			t.Skip("revocation unsupported by host")
		}
		t.Fatalf("AddRevocation: %v", err)
	}
	revoked, err := h.IsRevoked(ctx, sessionID)
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if !revoked {
		t.Fatalf("expected revoked=true")
	}
}

func testEpochBumpIfSupported(t *testing.T, factory HostFactory) {
	h := factory(t)
	defer cleanupHost(t, h)
	ctx := context.Background()

	scope := sessions.RevocationScope{UserID: "user-1"}
	before, err := h.GetEpoch(ctx, scope)
	if err != nil {
		if errors.Is(err, sessions.ErrRevocationUnsupported) {
			t.Skip("epoch unsupported by host")
			return
		}
		// Some hosts may return 0,nil for not-set; tolerate err==nil
	}
	after, err := h.BumpEpoch(ctx, scope)
	if err != nil {
		if errors.Is(err, sessions.ErrRevocationUnsupported) {
			t.Skip("epoch unsupported by host")
			return
		} else {
			t.Fatalf("BumpEpoch: %v", err)
		}
	}
	curr, err := h.GetEpoch(ctx, scope)
	if err != nil {
		t.Fatalf("GetEpoch: %v", err)
	}
	if curr < after {
		t.Fatalf("expected curr >= after, got curr=%d after=%d", curr, after)
	}
	if curr <= before {
		t.Fatalf("expected curr > before, got curr=%d before=%d", curr, before)
	}
}

// --- Manager integration tests ---

func testManagerIntegrationJWS(t *testing.T, factory HostFactory) {
	h := factory(t)
	defer cleanupHost(t, h)

	// Setup MemoryJWS with a single active Ed25519 key
	j := sessions.NewMemoryJWS()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	j.AddEd25519Key("k1", priv)
	if err := j.SetActive("k1"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}

	issuer := "test-issuer"
	mgr := sessions.NewManager(h, sessions.WithJWSSigner(j), sessions.WithIssuer(issuer), sessions.WithRevocationTTL(5*time.Minute))

	// Create a session for a user; token should embed epoch if supported
	ctx, cancel := context.WithCancel(context.Background())
	s, err := mgr.CreateSession(ctx, "user-abc")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if s.UserID() != "user-abc" {
		t.Fatalf("unexpected user id: %s", s.UserID())
	}
	sid := s.SessionID()
	if sid == "" {
		t.Fatalf("empty session id")
	}

	// Load should validate issuer, epoch, and per-sid revocation if supported
	s2, err := mgr.LoadSession(ctx, sid, "user-abc")
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if s2.SessionID() != sid {
		t.Fatalf("sid mismatch")
	}

	// Messaging round-trip through Session wrapper
	req := &jsonrpc.Request{JSONRPCVersion: "2.0", Method: "echo", ID: jsonrpc.NewRequestID(1)}
	b, _ := json.Marshal(req)

	recv := make(chan struct{}, 1)
	go func() {
		_ = s2.ConsumeMessages(ctx, "", func(ctx context.Context, id string, msg []byte) error {
			var got jsonrpc.Request
			_ = json.Unmarshal(msg, &got)
			if got.Method == req.Method {
				recv <- struct{}{}
			}
			return nil
		})
	}()
	time.Sleep(50 * time.Millisecond)
	if err := s2.WriteMessage(ctx, b); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	select {
	case <-recv:
		cancel() // stop the subscription goroutine
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive message via Session")
	}

	// Delete should add per-sid revocation, bump epoch (if supported), and cleanup resources
	if err := mgr.DeleteSession(ctx, sid); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// After delete, Load should fail either due to per-sid revocation or epoch bump
	if _, err := mgr.LoadSession(ctx, sid, "user-abc"); err == nil {
		t.Fatal("expected LoadSession to fail after DeleteSession")
	}
}

// --- Helpers ---

func cleanupHost(t *testing.T, h sessions.SessionHost) {
	// Best-effort cleanup for known session IDs used in tests
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ids := []string{"sess-1", "sess-2", "sess-3a", "sess-3b", "sess-4", "sess-5", "sess-6", "sess-7", "sess-8"}
	for _, id := range ids {
		_ = h.CleanupSession(ctx, id)
	}
	if closer, ok := h.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}
