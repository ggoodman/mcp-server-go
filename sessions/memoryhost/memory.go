package memoryhost

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/sessions"
)

// Host is an in-memory implementation of sessions.SessionHost.
type Host struct {
	mu       sync.RWMutex
	sessions map[string]*sessionData
	counter  atomic.Int64

	// revocation map: sessID -> expiresAt
	revokedMu sync.RWMutex
	revoked   map[string]time.Time

	// epoch map keyed by scope string
	epochMu sync.RWMutex
	epochs  map[string]int64
}

type sessionData struct {
	mu          sync.RWMutex
	messages    []message
	subscribers map[*subscription]struct{}
	awaits      map[string]*awaitState // correlationID -> await state
}

type message struct {
	id   string
	data []byte
}

type subscription struct {
	ctx      context.Context
	handler  sessions.MessageHandlerFunction
	startIdx int
	stopCh   chan struct{}
	errCh    chan error
	sd       *sessionData
}

func New() *Host {
	return &Host{
		sessions: make(map[string]*sessionData),
		revoked:  make(map[string]time.Time),
		epochs:   make(map[string]int64),
	}
}

// --- Messaging ---

func (h *Host) PublishSession(ctx context.Context, sessionID string, data []byte) (string, error) {
	evID := strconv.FormatInt(h.counter.Add(1), 10)
	msg := message{id: evID, data: append([]byte(nil), data...)}

	sd := h.ensureSession(sessionID)

	sd.mu.Lock()
	sd.messages = append(sd.messages, msg)
	idx := len(sd.messages) - 1
	// snapshot subscribers to notify
	subs := make([]*subscription, 0, len(sd.subscribers))
	for sub := range sd.subscribers {
		if idx >= sub.startIdx {
			subs = append(subs, sub)
		}
	}
	sd.mu.Unlock()

	for _, sub := range subs {
		s := sub
		select {
		case <-s.ctx.Done():
			continue
		case <-s.stopCh:
			continue
		default:
		}
		go func() {
			if err := s.handler(s.ctx, msg.id, msg.data); err != nil {
				select {
				case s.errCh <- err:
				default:
				}
			}
		}()
	}

	return evID, nil
}

func (h *Host) SubscribeSession(ctx context.Context, sessionID string, lastEventID string, handler sessions.MessageHandlerFunction) error {
	sd := h.ensureSession(sessionID)

	var startIdx int
	sd.mu.RLock()
	if lastEventID == "" {
		startIdx = len(sd.messages)
	} else {
		found := false
		for i := range sd.messages {
			if sd.messages[i].id == lastEventID {
				startIdx = i + 1
				found = true
				break
			}
		}
		if !found {
			sd.mu.RUnlock()
			return fmt.Errorf("last event id %s not found", lastEventID)
		}
	}
	sd.mu.RUnlock()

	sub := &subscription{ctx: ctx, handler: handler, startIdx: startIdx, stopCh: make(chan struct{}), errCh: make(chan error, 1), sd: sd}

	// register
	sd.mu.Lock()
	sd.subscribers[sub] = struct{}{}
	// gather replay
	var replay []message
	if startIdx < len(sd.messages) {
		replay = make([]message, len(sd.messages)-startIdx)
		copy(replay, sd.messages[startIdx:])
	}
	sd.mu.Unlock()

	// replay
	for _, m := range replay {
		select {
		case <-ctx.Done():
			sub.stop()
			return ctx.Err()
		case <-sub.stopCh:
			return nil
		case err := <-sub.errCh:
			sub.stop()
			return err
		default:
		}
		if err := handler(ctx, m.id, m.data); err != nil {
			sub.stop()
			return err
		}
	}

	// wait for next event or stop/cancel/handler error
	for {
		select {
		case <-ctx.Done():
			sub.stop()
			return ctx.Err()
		case <-sub.stopCh:
			return nil
		case err := <-sub.errCh:
			sub.stop()
			return err
		default:
			// Idle wait to avoid busy spin
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func (h *Host) CleanupSession(ctx context.Context, sessionID string) error {
	h.mu.Lock()
	sd, ok := h.sessions[sessionID]
	if ok {
		delete(h.sessions, sessionID)
	}
	h.mu.Unlock()
	if !ok {
		return nil
	}
	// Collect subscribers under lock, then stop them without holding the lock
	sd.mu.Lock()
	subs := make([]*subscription, 0, len(sd.subscribers))
	for sub := range sd.subscribers {
		subs = append(subs, sub)
	}
	// cancel all awaits
	for _, a := range sd.awaits {
		a.cancelLocked()
	}
	sd.awaits = make(map[string]*awaitState)
	sd.mu.Unlock()
	for _, sub := range subs {
		sub.stop()
	}
	return nil
}

// --- Revocation ---

func (h *Host) AddRevocation(ctx context.Context, sessionID string, ttl time.Duration) error {
	h.revokedMu.Lock()
	h.revoked[sessionID] = time.Now().Add(ttl)
	h.revokedMu.Unlock()
	return nil
}

func (h *Host) IsRevoked(ctx context.Context, sessionID string) (bool, error) {
	h.revokedMu.RLock()
	exp, ok := h.revoked[sessionID]
	h.revokedMu.RUnlock()
	if !ok {
		return false, nil
	}
	if time.Now().After(exp) {
		h.revokedMu.Lock()
		delete(h.revoked, sessionID)
		h.revokedMu.Unlock()
		return false, nil
	}
	return true, nil
}

func (h *Host) BumpEpoch(ctx context.Context, scope sessions.RevocationScope) (int64, error) {
	key := scopeKey(scope)
	h.epochMu.Lock()
	defer h.epochMu.Unlock()
	h.epochs[key] = h.epochs[key] + 1
	return h.epochs[key], nil
}

func (h *Host) GetEpoch(ctx context.Context, scope sessions.RevocationScope) (int64, error) {
	key := scopeKey(scope)
	h.epochMu.RLock()
	v := h.epochs[key]
	h.epochMu.RUnlock()
	return v, nil
}

func (h *Host) ensureSession(sessionID string) *sessionData {
	h.mu.Lock()
	defer h.mu.Unlock()
	sd, ok := h.sessions[sessionID]
	if !ok {
		sd = &sessionData{messages: make([]message, 0), subscribers: make(map[*subscription]struct{}), awaits: make(map[string]*awaitState)}
		h.sessions[sessionID] = sd
	}
	return sd
}

func (s *subscription) stop() {
	s.sd.mu.Lock()
	delete(s.sd.subscribers, s)
	s.sd.mu.Unlock()
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
}

func scopeKey(s sessions.RevocationScope) string {
	// Compose a stable key across provided dimensions.
	return "u=" + s.UserID + "|c=" + s.ClientID + "|t=" + s.TenantID
}

// Ensure interface compliance
var _ sessions.SessionHost = (*Host)(nil)

// --- Await/Fulfill implementation ---

type awaitState struct {
	ch   chan []byte
	done bool
}

func (a *awaitState) cancelLocked() {
	if !a.done {
		a.done = true
		close(a.ch)
	}
}

type awaiter struct {
	h             *Host
	sessionID     string
	correlationID string
}

func (a *awaiter) Recv(ctx context.Context) ([]byte, error) {
	sd := a.h.ensureSession(a.sessionID)
	sd.mu.RLock()
	st := sd.awaits[a.correlationID]
	ch := st.ch
	sd.mu.RUnlock()
	select {
	case <-ctx.Done():
		// best-effort cancel
		_ = a.Cancel(context.Background())
		return nil, ctx.Err()
	case data, ok := <-ch:
		if !ok {
			return nil, sessions.ErrAwaitCanceled
		}
		return data, nil
	}
}

func (a *awaiter) Cancel(ctx context.Context) error {
	sd := a.h.ensureSession(a.sessionID)
	sd.mu.Lock()
	if st, ok := sd.awaits[a.correlationID]; ok {
		st.cancelLocked()
		delete(sd.awaits, a.correlationID)
	}
	sd.mu.Unlock()
	return nil
}

func (h *Host) BeginAwait(ctx context.Context, sessionID, correlationID string, ttl time.Duration) (sessions.Awaiter, error) {
	sd := h.ensureSession(sessionID)
	sd.mu.Lock()
	if _, exists := sd.awaits[correlationID]; exists {
		sd.mu.Unlock()
		return nil, sessions.ErrAwaitExists
	}
	st := &awaitState{ch: make(chan []byte, 1)}
	sd.awaits[correlationID] = st
	sd.mu.Unlock()

	// TTL cleanup
	if ttl > 0 {
		timer := time.NewTimer(ttl)
		go func() {
			defer func() { _ = recover() }()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				sd.mu.Lock()
				if cur, ok := sd.awaits[correlationID]; ok && cur == st {
					st.cancelLocked()
					delete(sd.awaits, correlationID)
				}
				sd.mu.Unlock()
			}
		}()
	}

	return &awaiter{h: h, sessionID: sessionID, correlationID: correlationID}, nil
}

func (h *Host) Fulfill(ctx context.Context, sessionID, correlationID string, data []byte) (bool, error) {
	sd := h.ensureSession(sessionID)
	sd.mu.Lock()
	st, ok := sd.awaits[correlationID]
	if !ok {
		sd.mu.Unlock()
		return false, nil
	}
	if st.done {
		delete(sd.awaits, correlationID)
		sd.mu.Unlock()
		return false, nil
	}
	st.done = true
	delete(sd.awaits, correlationID)
	ch := st.ch
	sd.mu.Unlock()

	// Non-blocking send; channel has capacity 1
	select {
	case ch <- append([]byte(nil), data...):
	default:
		// If the channel is unexpectedly full, close it to wake receiver.
		close(ch)
		return false, nil
	}
	close(ch)
	return true, nil
}
