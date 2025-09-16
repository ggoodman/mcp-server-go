package memoryhost

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ggoodman/mcp-server-go/sessions"
)

// Host is an in-memory implementation of sessions.SessionHost.
// It is intended for tests and single-process servers. All data is ephemeral
// and lost on process exit. Safe for concurrent use.
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
	// server-internal events: topic -> subscribers
	eventSubs map[string]map[*eventSub]struct{}
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
	msgCh    chan message
	sd       *sessionData
	// ensure stop is executed only once
	stopOnce sync.Once
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
		select {
		case s.msgCh <- msg:
		default:
			// drop if subscriber is busy
		}
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

	sub := &subscription{ctx: ctx, handler: handler, startIdx: startIdx, stopCh: make(chan struct{}), errCh: make(chan error, 1), msgCh: make(chan message, 64), sd: sd}

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
		case m := <-sub.msgCh:
			if err := handler(ctx, m.id, m.data); err != nil {
				sub.stop()
				return err
			}
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
	// Collect all event subscribers under lock
	var evSubs []*eventSub
	for _, set := range sd.eventSubs {
		for sub := range set {
			evSubs = append(evSubs, sub)
		}
	}
	// Clear event subscriber map so subsequent stops don't contend on delete
	sd.eventSubs = make(map[string]map[*eventSub]struct{})
	sd.mu.Unlock()

	// Stop event subscribers outside the lock to avoid self-deadlock in eventSub.stop()
	for _, es := range evSubs {
		es.stop()
	}
	// Stop session message subscribers outside the lock as well
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
	// Increment epoch atomically under lock.
	h.epochs[key]++
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
		sd = &sessionData{messages: make([]message, 0), subscribers: make(map[*subscription]struct{}), eventSubs: make(map[string]map[*eventSub]struct{})}
		h.sessions[sessionID] = sd
	}
	return sd
}

func (s *subscription) stop() {
	s.stopOnce.Do(func() {
		s.sd.mu.Lock()
		delete(s.sd.subscribers, s)
		s.sd.mu.Unlock()
		close(s.stopCh)
	})
}

func scopeKey(s sessions.RevocationScope) string {
	// Compose a stable key across provided dimensions.
	return "u=" + s.UserID + "|c=" + s.ClientID + "|t=" + s.TenantID
}

// Ensure interface compliance: implement server-internal event pub/sub

type eventSub struct {
	ctx     context.Context
	handler sessions.EventHandlerFunction
	stopCh  chan struct{}
	sd      *sessionData
	topic   string
	// ensure stop is executed only once
	stopOnce sync.Once
}

func (e *eventSub) stop() {
	e.stopOnce.Do(func() {
		e.sd.mu.Lock()
		if set, ok := e.sd.eventSubs[e.topic]; ok {
			delete(set, e)
		}
		e.sd.mu.Unlock()
		close(e.stopCh)
	})
}

func (h *Host) PublishEvent(ctx context.Context, sessionID, topic string, payload []byte) error {
	sd := h.ensureSession(sessionID)
	sd.mu.RLock()
	subs := make([]*eventSub, 0)
	if set, ok := sd.eventSubs[topic]; ok {
		for sub := range set {
			subs = append(subs, sub)
		}
	}
	sd.mu.RUnlock()

	for _, s := range subs {
		sub := s
		select {
		case <-sub.ctx.Done():
			continue
		case <-sub.stopCh:
			continue
		default:
		}
		go func() { _ = sub.handler(sub.ctx, append([]byte(nil), payload...)) }()
	}
	return nil
}

func (h *Host) SubscribeEvents(ctx context.Context, sessionID, topic string, handler sessions.EventHandlerFunction) (func(), error) {
	sd := h.ensureSession(sessionID)
	sub := &eventSub{ctx: ctx, handler: handler, stopCh: make(chan struct{}), sd: sd, topic: topic}
	sd.mu.Lock()
	set := sd.eventSubs[topic]
	if set == nil {
		set = make(map[*eventSub]struct{})
		sd.eventSubs[topic] = set
	}
	set[sub] = struct{}{}
	sd.mu.Unlock()

	go func() {
		<-ctx.Done()
		sub.stop()
	}()
	return func() { sub.stop() }, nil
}
