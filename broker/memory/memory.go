package memory

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/ggoodman/mcp-streaming-http-go/broker"
	"github.com/ggoodman/mcp-streaming-http-go/internal/jsonrpc"
)

// Broker is an in-memory implementation of the broker.Broker interface.
// It provides namespace-based message isolation and ordered delivery guarantees
// within each namespace using in-memory storage.
type Broker struct {
	mu         sync.RWMutex
	namespaces map[string]*namespaceData
	counter    atomic.Int64
}

type namespaceData struct {
	mu          sync.RWMutex
	messages    []broker.MessageEnvelope
	subscribers map[*subscription]struct{}
}

type subscription struct {
	ctx       context.Context
	handler   broker.MessageHandler
	startIdx  int
	stopCh    chan struct{}
	errorCh   chan error
	namespace *namespaceData
}

// New creates a new in-memory broker instance.
func New() *Broker {
	return &Broker{
		namespaces: make(map[string]*namespaceData),
	}
}

// Publish creates envelope with generated event ID and publishes to namespace.
// Returns the generated event ID for the published message.
func (b *Broker) Publish(ctx context.Context, namespace string, message jsonrpc.Message) (string, error) {
	data := []byte(message)

	eventID := strconv.FormatInt(b.counter.Add(1), 10)
	envelope := broker.MessageEnvelope{
		ID:   eventID,
		Data: data,
	}

	b.mu.Lock()
	ns, exists := b.namespaces[namespace]
	if !exists {
		ns = &namespaceData{
			messages:    make([]broker.MessageEnvelope, 0),
			subscribers: make(map[*subscription]struct{}),
		}
		b.namespaces[namespace] = ns
	}
	b.mu.Unlock()

	ns.mu.Lock()
	ns.messages = append(ns.messages, envelope)
	messageIdx := len(ns.messages) - 1

	// Notify all active subscribers
	var subsToNotify []*subscription
	for sub := range ns.subscribers {
		if messageIdx >= sub.startIdx {
			subsToNotify = append(subsToNotify, sub)
		}
	}
	ns.mu.Unlock()

	// Deliver to subscribers outside of lock to avoid deadlock
	for _, sub := range subsToNotify {
		select {
		case <-sub.ctx.Done():
			// Subscriber context cancelled, skip
			continue
		case <-sub.stopCh:
			// Subscription stopped, skip
			continue
		default:
			go func(s *subscription) {
				if err := s.handler(s.ctx, envelope); err != nil {
					// Handler returned error, send to error channel
					select {
					case s.errorCh <- err:
					case <-s.ctx.Done():
					case <-s.stopCh:
					}
				}
			}(sub)
		}
	}

	return eventID, nil
}

// Subscribe to namespace messages, calling handler for each message.
// If lastEventID is empty, subscription starts from the next published message.
// If lastEventID is provided, subscription resumes from the message after that ID.
func (b *Broker) Subscribe(ctx context.Context, namespace string, lastEventID string, handler broker.MessageHandler) error {
	b.mu.Lock()
	ns, exists := b.namespaces[namespace]
	if !exists {
		ns = &namespaceData{
			messages:    make([]broker.MessageEnvelope, 0),
			subscribers: make(map[*subscription]struct{}),
		}
		b.namespaces[namespace] = ns
	}
	b.mu.Unlock()

	var startIdx int
	if lastEventID != "" {
		ns.mu.RLock()
		// Find the message after the last event ID
		found := false
		for i, msg := range ns.messages {
			if msg.ID == lastEventID {
				startIdx = i + 1
				found = true
				break
			}
		}
		if !found {
			ns.mu.RUnlock()
			return fmt.Errorf("last event ID %s not found in namespace %s", lastEventID, namespace)
		}
		ns.mu.RUnlock()
	} else {
		ns.mu.RLock()
		startIdx = len(ns.messages) // Start from next message
		ns.mu.RUnlock()
	}

	sub := &subscription{
		ctx:       ctx,
		handler:   handler,
		startIdx:  startIdx,
		stopCh:    make(chan struct{}),
		errorCh:   make(chan error, 1),
		namespace: ns,
	}

	// Register subscription
	ns.mu.Lock()
	ns.subscribers[sub] = struct{}{}

	// Replay any existing messages from startIdx
	var messagesToReplay []broker.MessageEnvelope
	if startIdx < len(ns.messages) {
		messagesToReplay = make([]broker.MessageEnvelope, len(ns.messages)-startIdx)
		copy(messagesToReplay, ns.messages[startIdx:])
	}
	ns.mu.Unlock()

	// Replay messages outside of lock
	for _, msg := range messagesToReplay {
		select {
		case <-ctx.Done():
			sub.stop()
			return ctx.Err()
		case <-sub.stopCh:
			return nil
		case err := <-sub.errorCh:
			sub.stop()
			return err
		default:
			if err := handler(ctx, msg); err != nil {
				sub.stop()
				return err
			}
		}
	}

	// Wait for context cancellation, subscription stop, or handler error
	select {
	case <-ctx.Done():
		sub.stop()
		return ctx.Err()
	case <-sub.stopCh:
		return nil
	case err := <-sub.errorCh:
		sub.stop()
		return err
	}
}

// Cleanup removes all resources associated with a namespace.
func (b *Broker) Cleanup(ctx context.Context, namespace string) error {
	b.mu.Lock()
	ns, exists := b.namespaces[namespace]
	if !exists {
		b.mu.Unlock()
		return nil
	}
	delete(b.namespaces, namespace)
	b.mu.Unlock()

	// Stop all subscribers
	ns.mu.Lock()
	for sub := range ns.subscribers {
		sub.stop()
	}
	ns.mu.Unlock()

	return nil
}

func (s *subscription) stop() {
	s.namespace.mu.Lock()
	delete(s.namespace.subscribers, s)
	s.namespace.mu.Unlock()

	select {
	case <-s.stopCh:
		// Already stopped
	default:
		close(s.stopCh)
	}
}
