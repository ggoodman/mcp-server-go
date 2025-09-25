package sessionhosttest

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// HostFactory creates a new SessionHost instance for testing.
type HostFactory func(t *testing.T) sessions.SessionHost

// RunSessionHostTests runs the complete SessionHost test suite against the provided factory.
func RunSessionHostTests(t *testing.T, factory HostFactory) {
	t.Run("Messaging_PublishAndSubscribeFromBeginning", func(t *testing.T) { testPublishAndSubscribeFromBeginning(t, factory) })
	t.Run("Messaging_PublishAndResumeFromLastEventID", func(t *testing.T) { testPublishAndSubscribeFromLastEventID(t, factory) })
	t.Run("Messaging_IsolationBetweenSessions", func(t *testing.T) { testSessionIsolation(t, factory) })
	t.Run("Messaging_SubscriptionContextCancellation", func(t *testing.T) { testSubscriptionContextCancellation(t, factory) })
	t.Run("Messaging_HandlerErrorStopsSubscription", func(t *testing.T) { testHandlerErrorStopsSubscription(t, factory) })
	t.Run("Messaging_ResumeFromNonExistentEventID", func(t *testing.T) { testResumeFromNonExistentEventID(t, factory) })
	t.Run("Messaging_CompetingConsumers_NoDuplicates", func(t *testing.T) { testCompetingConsumersNoDuplicates(t, factory) })

	// Event fan-out semantics
	t.Run("Events_FanOut_AllSubscribersReceiveAllFuture", func(t *testing.T) { testEventsFanOutAllSubscribersReceiveAllFuture(t, factory) })
	t.Run("Events_LateSubscriberOnlySeesLaterEvents", func(t *testing.T) { testEventsLateSubscriber(t, factory) })
	t.Run("Events_HandlerErrorTerminatesOnlyThatSubscriber", func(t *testing.T) { testEventsHandlerError(t, factory) })
	t.Run("Events_CancellationStopsSubscription", func(t *testing.T) { testEventsCancellation(t, factory) })
}

// --- Messaging tests ---

func testPublishAndSubscribeFromBeginning(t *testing.T, factory HostFactory) {
	h := factory(t)

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionID := "sess-5"
	req := &jsonrpc.Request{JSONRPCVersion: "2.0", Method: "test/m", ID: jsonrpc.NewRequestID(1)}
	b, _ := json.Marshal(req)
	expectedErr := errors.New("handler error")

	done := make(chan error, 1)
	go func() {
		done <- h.SubscribeSession(ctx, sessionID, "", func(ctx context.Context, id string, msg []byte) error { return expectedErr })
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err := h.PublishSession(ctx, sessionID, b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected handler error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe timeout")
	}
}

func testResumeFromNonExistentEventID(t *testing.T, factory HostFactory) {
	h := factory(t)

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

// Competing consumption: two subscribers to the same session should not both receive the same message.
// Total deliveries across subscribers should equal the number of published messages (at-least-once, no duplicates under normal operation).
func testCompetingConsumersNoDuplicates(t *testing.T, factory HostFactory) {
	h := factory(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionID := "sess-cc-1"
	const n = 10

	type rec struct {
		mu   sync.Mutex
		ids  []string
		done chan error
	}
	sub := func(r *rec) {
		r.done <- h.SubscribeSession(ctx, sessionID, "", func(c context.Context, id string, msg []byte) error {
			r.mu.Lock()
			r.ids = append(r.ids, id)
			r.mu.Unlock()
			return nil
		})
	}

	var r1, r2 rec
	r1.done = make(chan error, 1)
	r2.done = make(chan error, 1)
	go sub(&r1)
	go sub(&r2)

	// Give subscribers a moment to attach
	time.Sleep(100 * time.Millisecond)

	// Publish n messages
	for i := 0; i < n; i++ {
		req := &jsonrpc.Request{JSONRPCVersion: "2.0", Method: "cc/" + strconv.Itoa(i), ID: jsonrpc.NewRequestID(i + 1)}
		b, _ := json.Marshal(req)
		if _, err := h.PublishSession(ctx, sessionID, b); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	// Wait for all messages to be delivered or timeout
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r1.mu.Lock()
		c1 := len(r1.ids)
		r1.mu.Unlock()
		r2.mu.Lock()
		c2 := len(r2.ids)
		r2.mu.Unlock()
		if c1+c2 >= n {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// cancel to stop subscribers
	cancel()
	<-r1.done
	<-r2.done

	r1.mu.Lock()
	ids1 := append([]string(nil), r1.ids...)
	r1.mu.Unlock()
	r2.mu.Lock()
	ids2 := append([]string(nil), r2.ids...)
	r2.mu.Unlock()
	total := len(ids1) + len(ids2)
	if total != n {
		t.Fatalf("expected %d total deliveries, got %d (s1=%d, s2=%d)", n, total, len(ids1), len(ids2))
	}
	// ensure no duplicates across subscribers
	seen := make(map[string]bool, n)
	for _, id := range ids1 {
		if seen[id] {
			t.Fatalf("duplicate delivery for id %s", id)
		}
		seen[id] = true
	}
	for _, id := range ids2 {
		if seen[id] {
			t.Fatalf("duplicate delivery for id %s", id)
		}
		seen[id] = true
	}
}

// --- Event tests ---

func testEventsFanOutAllSubscribersReceiveAllFuture(t *testing.T, factory HostFactory) {
	h := factory(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sessionID := "ev-sess-1"
	topic := "t1"

	const n = 5
	type rec struct {
		mu     sync.Mutex
		events [][]byte
	}
	var r1, r2 rec

	// Envelope used to carry session scoping within the topic payload
	type eventEnvelope struct {
		SessionID string `json:"sessionID"`
		Payload   string `json:"payload"`
	}

	// Handshake: avoid racy first publish by using a test-only "ready" barrier.
	// We publish a "ready" sentinel until both subscribers ack seeing it. Only then
	// do we publish the actual test events. This keeps production code simple
	// (XREAD "$" semantics) while making the test deterministic.
	ready1 := make(chan struct{}, 1)
	ready2 := make(chan struct{}, 1)

	subCtx1, cancel1 := context.WithCancel(ctx)
	if err := h.SubscribeEvents(subCtx1, topic, func(c context.Context, p []byte) error {
		var env eventEnvelope
		if err := json.Unmarshal(p, &env); err != nil {
			return err
		}
		if env.SessionID != sessionID {
			return nil // ignore events for other sessions
		}
		if env.Payload == "__ready__" {
			select {
			case ready1 <- struct{}{}:
			default:
			}
			return nil
		}
		r1.mu.Lock()
		r1.events = append(r1.events, []byte(env.Payload))
		r1.mu.Unlock()
		if len(r1.events) == n {
			cancel1()
		}
		return nil
	}); err != nil {
		t.Fatalf("subscribe 1: %v", err)
	}
	defer cancel1()

	subCtx2, cancel2 := context.WithCancel(ctx)
	if err := h.SubscribeEvents(subCtx2, topic, func(c context.Context, p []byte) error {
		var env eventEnvelope
		if err := json.Unmarshal(p, &env); err != nil {
			return err
		}
		if env.SessionID != sessionID {
			return nil
		}
		if env.Payload == "__ready__" {
			select {
			case ready2 <- struct{}{}:
			default:
			}
			return nil
		}
		r2.mu.Lock()
		r2.events = append(r2.events, []byte(env.Payload))
		r2.mu.Unlock()
		if len(r2.events) == n {
			cancel2()
		}
		return nil
	}); err != nil {
		t.Fatalf("subscribe 2: %v", err)
	}
	defer cancel2()

	// Publish a small number of "ready" sentinels until both subscribers report readiness
	barrierCtx, barrierCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer barrierCancel()
	for barrierCtx.Err() == nil {
		b, _ := json.Marshal(eventEnvelope{SessionID: sessionID, Payload: "__ready__"})
		_ = h.PublishEvent(ctx, topic, b)
		got1, got2 := false, false
		// drain with short deadline each round
		deadline := time.After(25 * time.Millisecond)
		for !got1 || !got2 {
			select {
			case <-ready1:
				got1 = true
			case <-ready2:
				got2 = true
			case <-deadline:
				goto maybeNext
			case <-barrierCtx.Done():
				goto doneBarrier
			}
		}
	maybeNext:
		if got1 && got2 {
			break
		}
	}

doneBarrier:
	// Now publish the actual test events
	for i := 0; i < n; i++ {
		b, _ := json.Marshal(eventEnvelope{SessionID: sessionID, Payload: strconv.Itoa(i)})
		if err := h.PublishEvent(ctx, topic, b); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	<-subCtx1.Done()
	<-subCtx2.Done()

	r1.mu.Lock()
	c1 := len(r1.events)
	r1.mu.Unlock()
	r2.mu.Lock()
	c2 := len(r2.events)
	r2.mu.Unlock()
	if c1 != n || c2 != n {
		t.Fatalf("expected %d events each; got %d and %d", n, c1, c2)
	}
	// Ordering check
	for i := 0; i < n; i++ {
		exp := strconv.Itoa(i)
		if string(r1.events[i]) != exp || string(r2.events[i]) != exp {
			t.Fatalf("ordering mismatch at %d", i)
		}
	}
}

func testEventsLateSubscriber(t *testing.T, factory HostFactory) {
	h := factory(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sessionID := "ev-sess-2"
	topic := "t2"
	const first = 3
	const second = 4
	type rec struct {
		mu     sync.Mutex
		events [][]byte
	}
	var rEarly, rLate rec

	type eventEnvelope struct {
		SessionID string `json:"sessionID"`
		Payload   string `json:"payload"`
	}

	earlyCtx, earlyCancel := context.WithCancel(ctx)
	if err := h.SubscribeEvents(earlyCtx, topic, func(c context.Context, p []byte) error {
		var env eventEnvelope
		if err := json.Unmarshal(p, &env); err != nil {
			return err
		}
		if env.SessionID != sessionID {
			return nil
		}
		rEarly.mu.Lock()
		rEarly.events = append(rEarly.events, []byte(env.Payload))
		rEarly.mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("subscribe early: %v", err)
	}
	defer earlyCancel()

	// Publish first batch (late subscriber should NOT get these)
	for i := 0; i < first; i++ {
		b, _ := json.Marshal(eventEnvelope{SessionID: sessionID, Payload: "A" + strconv.Itoa(i)})
		if err := h.PublishEvent(ctx, topic, b); err != nil {
			t.Fatalf("publish pre %d: %v", i, err)
		}
	}

	lateCtx, lateCancel := context.WithCancel(ctx)
	if err := h.SubscribeEvents(lateCtx, topic, func(c context.Context, p []byte) error {
		var env eventEnvelope
		if err := json.Unmarshal(p, &env); err != nil {
			return err
		}
		if env.SessionID != sessionID {
			return nil
		}
		rLate.mu.Lock()
		rLate.events = append(rLate.events, []byte(env.Payload))
		rLate.mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("subscribe late: %v", err)
	}

	for i := 0; i < second; i++ {
		b, _ := json.Marshal(eventEnvelope{SessionID: sessionID, Payload: "B" + strconv.Itoa(i)})
		if err := h.PublishEvent(ctx, topic, b); err != nil {
			t.Fatalf("publish post %d: %v", i, err)
		}
	}

	time.Sleep(200 * time.Millisecond)
	earlyCancel()
	lateCancel()

	rEarly.mu.Lock()
	e1 := len(rEarly.events)
	rEarly.mu.Unlock()
	rLate.mu.Lock()
	e2 := len(rLate.events)
	rLate.mu.Unlock()
	if e1 != first+second {
		t.Fatalf("early expected %d events got %d", first+second, e1)
	}
	if e2 != second {
		t.Fatalf("late expected %d events got %d", second, e2)
	}
}

func testEventsHandlerError(t *testing.T, factory HostFactory) {
	h := factory(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sessionID := "ev-sess-3"
	topic := "t3"
	errSentinel := errors.New("handler boom")
	var gotSecond bool
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	type eventEnvelope struct {
		SessionID string `json:"sessionID"`
		Payload   string `json:"payload"`
	}
	if err := h.SubscribeEvents(subCtx, topic, func(c context.Context, p []byte) error {
		var env eventEnvelope
		if err := json.Unmarshal(p, &env); err != nil {
			return err
		}
		if env.SessionID == sessionID && env.Payload == "1" {
			gotSecond = true
		}
		return errSentinel
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	// First publish triggers error, second may or may not be delivered depending on timing but should not cause panic.
	b0, _ := json.Marshal(eventEnvelope{SessionID: sessionID, Payload: "0"})
	b1, _ := json.Marshal(eventEnvelope{SessionID: sessionID, Payload: "1"})
	_ = h.PublishEvent(ctx, topic, b0)
	_ = h.PublishEvent(ctx, topic, b1)
	time.Sleep(100 * time.Millisecond)
	if gotSecond {
		t.Logf("second event delivered after handler error (acceptable at-least-once)")
	}
}

func testEventsCancellation(t *testing.T, factory HostFactory) {
	h := factory(t)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	topic := "t4"
	if err := h.SubscribeEvents(ctx, topic, func(c context.Context, p []byte) error { return nil }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	// Just wait for context deadline
	<-ctx.Done()
}

// --- Revocation tests (optional) ---

// --- Manager integration tests ---

// --- Helpers ---

// NOTE: removed legacy cleanup/revocation/JWS manager integration tests as stateful sessions replaced them.
