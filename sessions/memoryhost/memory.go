package memoryhost

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/google/uuid"
)

// Interface compliance
var _ sessions.SessionHost = (*Host)(nil)

// Host is an in-memory implementation of sessions.SessionHost.
// Intended for tests and single-process servers. All data is ephemeral
// and lost on process exit. Safe for concurrent use.
type Host struct {
	mu      sync.RWMutex
	streams map[string]*sessionStream            // sessionID -> ordered message stream
	metas   map[string]*sessions.SessionMetadata // sessionID -> metadata
	kv      map[string]map[string][]byte         // sessionID -> (key -> value)
	events  map[string]*eventStream              // topic -> event fan-out
}

// sessionStream stores ordered messages for a session plus waiters for new data.
type sessionStream struct {
	mu        sync.Mutex
	messages  []message
	nextID    int64
	waiters   []chan struct{}
	closed    bool
	delivered map[string]bool // messages acked (handler returned nil)
	inflight  map[string]bool // messages currently being processed
}

type message struct {
	id   string
	data []byte
}

// eventStream fan-outs events published after subscription time only.
type eventStream struct {
	mu     sync.Mutex
	subs   map[int]chan []byte
	nextID int
}

// New constructs a new in-memory Host instance.
func New() *Host {
	return &Host{
		streams: make(map[string]*sessionStream),
		metas:   make(map[string]*sessions.SessionMetadata),
		kv:      make(map[string]map[string][]byte),
		events:  make(map[string]*eventStream),
	}
}

// --- Messaging (ordered per session with resumable IDs) ---

func (h *Host) getOrCreateStream(sessionID string) *sessionStream {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.streams[sessionID]; ok {
		return s
	}
	s := &sessionStream{delivered: make(map[string]bool), inflight: make(map[string]bool)}
	h.streams[sessionID] = s
	return s
}

// PublishSession appends a message to the per-session ordered stream.
// IDs are monotonically increasing decimal strings starting at 1.
func (h *Host) PublishSession(ctx context.Context, sessionID string, data []byte) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	s := h.getOrCreateStream(sessionID)
	s.mu.Lock()
	s.nextID++
	id := strconv.FormatInt(s.nextID, 10)
	// copy payload to avoid caller mutation
	cp := append([]byte(nil), data...)
	s.messages = append(s.messages, message{id: id, data: cp})
	// wake waiters (broadcast; claim under lock ensures single delivery)
	for _, ch := range s.waiters {
		select { // non-blocking signal
		case ch <- struct{}{}:
		default:
		}
	}
	s.waiters = nil
	s.mu.Unlock()
	return id, nil
}

// SubscribeSession delivers messages strictly after lastEventID (if provided) or only
// future messages when lastEventID == "". It blocks until context cancellation or handler error.
func (h *Host) SubscribeSession(ctx context.Context, sessionID string, lastEventID string, handler sessions.MessageHandlerFunction) error {
	s := h.getOrCreateStream(sessionID)

	// Local cursor to start-after semantics, tracked as index not string to avoid lexicographic issues
	s.mu.Lock()
	lastIdx := -1
	if lastEventID != "" {
		for i := range s.messages {
			if s.messages[i].id == lastEventID {
				lastIdx = i
				break
			}
		}
		if lastIdx < 0 {
			lastIdx = len(s.messages) - 1 // future only
		}
	} else {
		lastIdx = len(s.messages) - 1 // future only
	}
	s.mu.Unlock()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Try to claim the next available message strictly after lastIdx
		var claimed *message
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return errors.New("session closed")
		}
		for i := range s.messages {
			if i <= lastIdx {
				continue
			}
			id := s.messages[i].id
			if s.delivered[id] || s.inflight[id] {
				continue
			}
			s.inflight[id] = true
			claimed = &s.messages[i]
			break
		}
		if claimed == nil {
			// register waiter and wait for new messages
			waiter := make(chan struct{}, 1)
			s.waiters = append(s.waiters, waiter)
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-waiter:
				// loop and attempt to claim again
				continue
			}
		}
		s.mu.Unlock()

		// Process outside lock
		msgID := claimed.id
		data := append([]byte(nil), claimed.data...)
		if err := handler(ctx, msgID, data); err != nil {
			// release claim for redelivery
			s.mu.Lock()
			delete(s.inflight, msgID)
			s.mu.Unlock()
			return err
		}
		// ack delivery and advance local cursor
		s.mu.Lock()
		delete(s.inflight, msgID)
		s.delivered[msgID] = true
		// advance lastIdx to this message index
		for i := range s.messages {
			if s.messages[i].id == msgID {
				lastIdx = i
				break
			}
		}
		s.mu.Unlock()
	}
}

// --- Event fan-out (no replay) ---

func (h *Host) eventKey(topic string) string { return topic }

func (h *Host) getOrCreateEventStream(topic string) *eventStream {
	key := h.eventKey(topic)
	h.mu.Lock()
	defer h.mu.Unlock()
	if es, ok := h.events[key]; ok {
		return es
	}
	es := &eventStream{subs: make(map[int]chan []byte)}
	h.events[key] = es
	return es
}

func (h *Host) PublishEvent(ctx context.Context, topic string, payload []byte) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	es := h.getOrCreateEventStream(topic)
	es.mu.Lock()
	// snapshot subscriber channels
	chans := make([]chan []byte, 0, len(es.subs))
	for _, ch := range es.subs {
		chans = append(chans, ch)
	}
	es.mu.Unlock()
	// deliver outside lock to avoid blocking other operations
	for _, ch := range chans {
		// copy payload for each subscriber (independent delivery)
		cp := append([]byte(nil), payload...)
		// Non-blocking send: drop if subscriber is slow or has unsubscribed.
		// This avoids races with subscriber teardown and prevents publisher stalls.
		select {
		case ch <- cp:
		default:
			// drop
		}
	}
	return nil
}

func (h *Host) SubscribeEvents(ctx context.Context, topic string, handler sessions.EventHandlerFunction) error {
	es := h.getOrCreateEventStream(topic)
	es.mu.Lock()
	es.nextID++
	id := es.nextID
	ch := make(chan []byte, 32) // small buffer
	es.subs[id] = ch
	es.mu.Unlock()

	// Goroutine to process events; returns on context cancel or handler error
	go func() {
		defer func() {
			es.mu.Lock()
			delete(es.subs, id)
			es.mu.Unlock()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case p, ok := <-ch:
				if !ok {
					return
				}
				if err := handler(ctx, p); err != nil {
					return
				}
			}
		}
	}()
	return nil
}

// --- Metadata lifecycle ---

func (h *Host) CreateSession(ctx context.Context, meta *sessions.SessionMetadata) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Respect provided SessionID; generate a new one if empty.
	sessID := meta.SessionID
	if sessID == "" {
		sessID = uuid.NewString()
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.metas[sessID]; exists {
		return errors.New("session exists")
	}

	meta.SessionID = sessID
	cp := *meta
	h.metas[sessID] = &cp
	return nil
}

func (h *Host) GetSession(ctx context.Context, sessionID string) (*sessions.SessionMetadata, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	m, ok := h.metas[sessionID]
	if !ok {
		return nil, sessions.ErrSessionNotFound
	}
	cp := *m
	return &cp, nil
}

func (h *Host) MutateSession(ctx context.Context, sessionID string, fn func(*sessions.SessionMetadata) error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.metas[sessionID]
	if !ok {
		return sessions.ErrSessionNotFound
	}
	if err := fn(m); err != nil {
		return err
	}
	m.UpdatedAt = time.Now().UTC()
	return nil
}

func (h *Host) TouchSession(ctx context.Context, sessionID string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.metas[sessionID]
	if !ok {
		return sessions.ErrSessionNotFound
	}
	now := time.Now().UTC()
	m.LastAccess = now
	m.UpdatedAt = now
	return nil
}

func (h *Host) DeleteSession(ctx context.Context, sessionID string) error {
	// best-effort; ignore ctx for cleanup aside from early cancel
	if ctx.Err() != nil {
		return ctx.Err()
	}
	h.mu.Lock()
	// capture stream pointer to signal subscribers after unlocking host mutex
	s := h.streams[sessionID]
	// delete metadata
	delete(h.metas, sessionID)
	// delete kv
	delete(h.kv, sessionID)
	// delete message stream
	delete(h.streams, sessionID)
	h.mu.Unlock()

	// If there was an active stream, mark it closed and wake any waiters
	if s != nil {
		s.mu.Lock()
		s.closed = true
		// Snapshot waiters and clear to avoid double signaling
		waiters := append([]chan struct{}(nil), s.waiters...)
		s.waiters = nil
		s.mu.Unlock()
		for _, ch := range waiters {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
	return nil
}

// --- KV storage ---

func (h *Host) PutSessionData(ctx context.Context, sessionID, key string, value []byte) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.kv[sessionID]
	if !ok {
		m = make(map[string][]byte)
		h.kv[sessionID] = m
	}
	m[key] = append([]byte(nil), value...)
	return nil
}

func (h *Host) GetSessionData(ctx context.Context, sessionID, key string) ([]byte, bool, error) {
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	m, ok := h.kv[sessionID]
	if !ok {
		return nil, false, nil
	}
	v, ok := m[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func (h *Host) DeleteSessionData(ctx context.Context, sessionID, key string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if m, ok := h.kv[sessionID]; ok {
		delete(m, key)
		if len(m) == 0 {
			delete(h.kv, sessionID)
		}
	}
	return nil
}
