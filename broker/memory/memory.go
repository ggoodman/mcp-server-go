// Package memory provides an in-memory implementation of the broker.Broker interface
// using Go channels for message delivery. This implementation is suitable for
// single-node deployments and testing scenarios.
package memory

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/ggoodman/mcp-streaming-http-go/broker"
	"github.com/ggoodman/mcp-streaming-http-go/internal/jsonrpc"
)

// Broker implements broker.Broker using in-memory channels and storage.
// It provides namespace isolation and ordered message delivery within each namespace.
// This implementation is not suitable for multi-node deployments as state is local.
type Broker struct {
	mu           sync.RWMutex
	namespaces   map[string]*namespace
	eventCounter atomic.Int64
}

// namespace represents an isolated message queue with its subscribers
type namespace struct {
	mu          sync.RWMutex
	messages    []broker.MessageEnvelope
	subscribers map[*subscription]struct{}
	closed      bool
}

// subscription represents an active subscription to a namespace
type subscription struct {
	namespace   *namespace
	lastEventID string
	ch          chan broker.MessageEnvelope
	ctx         context.Context
	cancel      context.CancelFunc
	closed      atomic.Bool
}

// New creates a new memory-based broker instance.
func New() *Broker {
	return &Broker{
		namespaces: make(map[string]*namespace),
	}
}

// Publish implements broker.Broker.Publish
func (b *Broker) Publish(ctx context.Context, namespaceName string, message jsonrpc.Message) (eventID string, err error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	// Generate unique event ID
	eventID = strconv.FormatInt(b.eventCounter.Add(1), 10)

	// Create message envelope
	envelope := broker.MessageEnvelope{
		ID:   eventID,
		Data: []byte(message),
	}

	b.mu.Lock()
	ns, exists := b.namespaces[namespaceName]
	if !exists {
		ns = &namespace{
			messages:    make([]broker.MessageEnvelope, 0),
			subscribers: make(map[*subscription]struct{}),
		}
		b.namespaces[namespaceName] = ns
	}
	b.mu.Unlock()

	ns.mu.Lock()
	defer ns.mu.Unlock()

	if ns.closed {
		return "", fmt.Errorf("namespace %q has been cleaned up", namespaceName)
	}

	// Store message
	ns.messages = append(ns.messages, envelope)

	// Notify subscribers
	for sub := range ns.subscribers {
		select {
		case sub.ch <- envelope:
		case <-sub.ctx.Done():
			// Subscriber context cancelled, remove from set
			delete(ns.subscribers, sub)
		default:
			// Channel is full, skip this subscriber
			// In a production implementation, you might want to handle this differently
		}
	}

	return eventID, nil
}

// Subscribe implements broker.Broker.Subscribe
func (b *Broker) Subscribe(ctx context.Context, namespaceName string, lastEventID string) (broker.MessageStream, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	b.mu.Lock()
	ns, exists := b.namespaces[namespaceName]
	if !exists {
		ns = &namespace{
			messages:    make([]broker.MessageEnvelope, 0),
			subscribers: make(map[*subscription]struct{}),
		}
		b.namespaces[namespaceName] = ns
	}
	b.mu.Unlock()

	ns.mu.Lock()
	defer ns.mu.Unlock()

	if ns.closed {
		return nil, fmt.Errorf("namespace %q has been cleaned up", namespaceName)
	}

	subCtx, cancel := context.WithCancel(ctx)
	sub := &subscription{
		namespace:   ns,
		lastEventID: lastEventID,
		ch:          make(chan broker.MessageEnvelope, 100), // Buffer to avoid blocking publishers
		ctx:         subCtx,
		cancel:      cancel,
	}

	// Add to subscribers set
	ns.subscribers[sub] = struct{}{}

	// Send historical messages if resuming from a specific event ID
	if lastEventID != "" {
		startIdx := -1
		for i, msg := range ns.messages {
			if msg.ID == lastEventID {
				startIdx = i + 1 // Start from message after lastEventID
				break
			}
		}

		if startIdx >= 0 {
			// Send historical messages in order
			for i := startIdx; i < len(ns.messages); i++ {
				select {
				case sub.ch <- ns.messages[i]:
				case <-sub.ctx.Done():
					delete(ns.subscribers, sub)
					return nil, sub.ctx.Err()
				}
			}
		}
	}

	return sub, nil
}

// Cleanup implements broker.Broker.Cleanup
func (b *Broker) Cleanup(ctx context.Context, namespaceName string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	b.mu.Lock()
	ns, exists := b.namespaces[namespaceName]
	if !exists {
		b.mu.Unlock()
		return nil // Nothing to clean up
	}
	delete(b.namespaces, namespaceName)
	b.mu.Unlock()

	ns.mu.Lock()
	defer ns.mu.Unlock()

	ns.closed = true

	// Close all subscribers
	for sub := range ns.subscribers {
		sub.cancel()
		close(sub.ch)
	}

	// Clear the subscribers map
	ns.subscribers = make(map[*subscription]struct{})
	ns.messages = nil

	return nil
}

// Next implements broker.MessageStream.Next
func (s *subscription) Next(ctx context.Context) (broker.MessageEnvelope, error) {
	if s.closed.Load() {
		return broker.MessageEnvelope{}, io.EOF
	}

	select {
	case msg, ok := <-s.ch:
		if !ok {
			return broker.MessageEnvelope{}, io.EOF
		}
		s.lastEventID = msg.ID
		return msg, nil
	case <-ctx.Done():
		return broker.MessageEnvelope{}, ctx.Err()
	case <-s.ctx.Done():
		return broker.MessageEnvelope{}, s.ctx.Err()
	}
}

// Close implements broker.MessageStream.Close
func (s *subscription) Close() error {
	if s.closed.CompareAndSwap(false, true) {
		s.namespace.mu.Lock()
		delete(s.namespace.subscribers, s)
		s.namespace.mu.Unlock()

		s.cancel()
		close(s.ch)
	}
	return nil
}

// Compile-time interface checks
var (
	_ broker.Broker        = (*Broker)(nil)
	_ broker.MessageStream = (*subscription)(nil)
)
