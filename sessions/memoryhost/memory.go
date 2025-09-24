package memoryhost

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/ggoodman/mcp-server-go/sessions"
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
	mu       sync.Mutex
	messages []message
	nextID   int64
	waiters  []chan struct{}
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
	s := &sessionStream{}
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
	// wake waiters
	if len(s.waiters) > 0 {
		for _, ch := range s.waiters {
			select { // non-blocking signal
			case ch <- struct{}{}:
			default:
			}
		}
		s.waiters = nil
	}
	s.mu.Unlock()
	return id, nil
}

// SubscribeSession delivers messages strictly after lastEventID (if provided) or only
// future messages when lastEventID == "". It blocks until context cancellation or handler error.
func (h *Host) SubscribeSession(ctx context.Context, sessionID string, lastEventID string, handler sessions.MessageHandlerFunction) error {
	s := h.getOrCreateStream(sessionID)

	// Determine starting index
	s.mu.Lock()
	startIdx := 0
	if lastEventID == "" { // only future messages
		startIdx = len(s.messages)
	} else {
		// find lastEventID
		found := -1
		for i := range s.messages {
			if s.messages[i].id == lastEventID {
				found = i
				break
			}
		}
		if found >= 0 {
			startIdx = found + 1
		} else {
			// Treat unknown ID as requesting only future messages (acceptable per test contract)
			startIdx = len(s.messages)
		}
	}
	s.mu.Unlock()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Fast path: drain any pending messages >= startIdx
		s.mu.Lock()
		if startIdx < len(s.messages) {
			msg := s.messages[startIdx]
			startIdx++
			s.mu.Unlock()
			if err := handler(ctx, msg.id, append([]byte(nil), msg.data...)); err != nil {
				return err
			}
			continue
		}
		// No messages; register waiter then wait on context or signal
		waiter := make(chan struct{}, 1)
		s.waiters = append(s.waiters, waiter)
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-waiter:
			// loop; new message available
		}
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
		select {
		case ch <- cp:
		case <-ctx.Done():
			return ctx.Err()
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
			close(ch)
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
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.metas[meta.SessionID]; exists {
		return errors.New("session exists")
	}
	cp := *meta
	h.metas[meta.SessionID] = &cp
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
		return nil, errors.New("not found")
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
		return errors.New("not found")
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
		return errors.New("not found")
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
	// delete metadata
	delete(h.metas, sessionID)
	// delete kv
	delete(h.kv, sessionID)
	// delete message stream
	delete(h.streams, sessionID)
	h.mu.Unlock()
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

func (h *Host) GetSessionData(ctx context.Context, sessionID, key string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	m, ok := h.kv[sessionID]
	if !ok {
		return nil, errors.New("not found")
	}
	v, ok := m[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), v...), nil
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
