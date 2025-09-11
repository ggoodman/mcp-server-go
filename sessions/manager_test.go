package sessions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"
)

// testHost is a minimal in-memory SessionHost for tests.
type testHost struct {
	mu          sync.Mutex
	epochByUser map[string]int64
	revoked     map[string]time.Time
	cleaned     []string
	awaits      map[string]chan []byte   // key: sessionID+"|"+corr
	evSub       map[string][]chan []byte // key: sessionID+"|"+topic
}

func newTestHost() *testHost {
	return &testHost{
		epochByUser: make(map[string]int64),
		revoked:     make(map[string]time.Time),
		cleaned:     []string{},
		awaits:      make(map[string]chan []byte),
		evSub:       make(map[string][]chan []byte),
	}
}

func (h *testHost) PublishSession(ctx context.Context, sessionID string, data []byte) (string, error) {
	return "", nil
}
func (h *testHost) SubscribeSession(ctx context.Context, sessionID string, lastEventID string, handler MessageHandlerFunction) error {
	return nil
}
func (h *testHost) CleanupSession(ctx context.Context, sessionID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleaned = append(h.cleaned, sessionID)
	return nil
}
func (h *testHost) AddRevocation(ctx context.Context, sessionID string, ttl time.Duration) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.revoked[sessionID] = time.Now().Add(ttl)
	return nil
}
func (h *testHost) IsRevoked(ctx context.Context, sessionID string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	until, ok := h.revoked[sessionID]
	if !ok {
		return false, nil
	}
	if time.Now().Before(until) {
		return true, nil
	}
	delete(h.revoked, sessionID)
	return false, nil
}
func (h *testHost) BumpEpoch(ctx context.Context, scope RevocationScope) (int64, error) {
	if scope.UserID == "" {
		return 0, errors.New("missing user id")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.epochByUser[scope.UserID] = h.epochByUser[scope.UserID] + 1
	return h.epochByUser[scope.UserID], nil
}
func (h *testHost) GetEpoch(ctx context.Context, scope RevocationScope) (int64, error) {
	if scope.UserID == "" {
		return 0, errors.New("missing user id")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.epochByUser[scope.UserID], nil
}

func (h *testHost) BeginAwait(ctx context.Context, sessionID, correlationID string, ttl time.Duration) (Awaiter, error) {
	key := sessionID + "|" + correlationID
	h.mu.Lock()
	if _, exists := h.awaits[key]; exists {
		h.mu.Unlock()
		return nil, ErrAwaitExists
	}
	ch := make(chan []byte, 1)
	h.awaits[key] = ch
	h.mu.Unlock()

	return &simpleAwaiter{host: h, key: key}, nil
}

type simpleAwaiter struct {
	host *testHost
	key  string
}

func (a *simpleAwaiter) Recv(ctx context.Context) ([]byte, error) {
	a.host.mu.Lock()
	ch := a.host.awaits[a.key]
	a.host.mu.Unlock()
	if ch == nil {
		return nil, ErrAwaitCanceled
	}
	select {
	case <-ctx.Done():
		_ = a.Cancel(context.Background())
		return nil, ctx.Err()
	case b, ok := <-ch:
		if !ok {
			return nil, ErrAwaitCanceled
		}
		return b, nil
	}
}

func (a *simpleAwaiter) Cancel(ctx context.Context) error {
	a.host.mu.Lock()
	if ch, ok := a.host.awaits[a.key]; ok {
		delete(a.host.awaits, a.key)
		close(ch)
	}
	a.host.mu.Unlock()
	return nil
}

func (h *testHost) Fulfill(ctx context.Context, sessionID, correlationID string, data []byte) (bool, error) {
	key := sessionID + "|" + correlationID
	h.mu.Lock()
	ch, ok := h.awaits[key]
	if ok {
		delete(h.awaits, key)
	}
	h.mu.Unlock()
	if !ok {
		return false, nil
	}
	select {
	case ch <- data:
	default:
	}
	close(ch)
	return true, nil
}

func (h *testHost) PublishEvent(ctx context.Context, sessionID, topic string, payload []byte) error {
	key := sessionID + "|" + topic
	h.mu.Lock()
	subs := append([]chan []byte(nil), h.evSub[key]...)
	h.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- append([]byte(nil), payload...):
		default:
		}
	}
	return nil
}

func (h *testHost) SubscribeEvents(ctx context.Context, sessionID, topic string, handler EventHandlerFunction) (func(), error) {
	key := sessionID + "|" + topic
	ch := make(chan []byte, 4)
	h.mu.Lock()
	h.evSub[key] = append(h.evSub[key], ch)
	h.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-ch:
				_ = handler(ctx, b)
			}
		}
	}()
	return func() {
		h.mu.Lock()
		subs := h.evSub[key]
		for i, c := range subs {
			if c == ch {
				h.evSub[key] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		h.mu.Unlock()
		close(ch)
		<-done
	}, nil
}

func newTestJWS(t *testing.T) *MemoryJWS {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	_ = pub // not needed directly
	if err != nil {
		t.Fatalf("failed to gen ed25519 key: %v", err)
	}
	jws := NewMemoryJWS()
	jws.AddEd25519Key("kid-test", priv)
	if err := jws.SetActive("kid-test"); err != nil {
		t.Fatalf("set active kid: %v", err)
	}
	return jws
}

func TestCreateLoadSession_JWS_IssuerAndEpoch(t *testing.T) {
	ctx := context.Background()
	host := newTestHost()
	jws := newTestJWS(t)

	user := "user-1"
	issA := "https://issuer-a.example"
	issB := "https://issuer-b.example"

	// Manager A with issuer A
	mgrA := NewManager(host, WithJWSSigner(jws), WithIssuer(issA), WithRevocationTTL(10*time.Minute))
	sess, err := mgrA.CreateSession(ctx, user)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sid := sess.SessionID()

	// Load with same issuer succeeds
	if _, err := mgrA.LoadSession(ctx, sid, user); err != nil {
		t.Fatalf("LoadSession with same issuer: %v", err)
	}

	// Manager B with a different issuer must reject the token
	mgrB := NewManager(host, WithJWSSigner(jws), WithIssuer(issB))
	if _, err := mgrB.LoadSession(ctx, sid, user); err == nil {
		t.Fatalf("expected issuer mismatch to fail, got nil error")
	}
}

func TestLoadSession_EpochRevokedByBump(t *testing.T) {
	ctx := context.Background()
	host := newTestHost()
	jws := newTestJWS(t)
	mgr := NewManager(host, WithJWSSigner(jws), WithIssuer("https://issuer.example"))

	user := "user-2"
	sess, err := mgr.CreateSession(ctx, user)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := host.BumpEpoch(ctx, RevocationScope{UserID: user}); err != nil {
		t.Fatalf("BumpEpoch: %v", err)
	}

	if _, err := mgr.LoadSession(ctx, sess.SessionID(), user); err == nil {
		t.Fatalf("expected load to fail after epoch bump")
	}
}

func TestDeleteSession_AddsRevocationAndCleanup(t *testing.T) {
	ctx := context.Background()
	host := newTestHost()
	jws := newTestJWS(t)
	mgr := NewManager(host, WithJWSSigner(jws), WithIssuer("https://issuer.example"), WithRevocationTTL(1*time.Hour))

	user := "user-3"
	sess, err := mgr.CreateSession(ctx, user)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sid := sess.SessionID()

	if err := mgr.DeleteSession(ctx, sid); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Revocation should be recorded
	if revoked, err := host.IsRevoked(ctx, sid); err != nil || !revoked {
		t.Fatalf("expected sid to be revoked; revoked=%v err=%v", revoked, err)
	}

	// Cleanup should be called
	host.mu.Lock()
	cleaned := append([]string(nil), host.cleaned...)
	host.mu.Unlock()
	if len(cleaned) == 0 || cleaned[len(cleaned)-1] != sid {
		t.Fatalf("expected cleanup to include %s; got %v", sid, cleaned)
	}

	// Loading after delete should fail due to revocation
	if _, err := mgr.LoadSession(ctx, sid, user); err == nil {
		t.Fatalf("expected load to fail due to per-sid revocation")
	}
}
