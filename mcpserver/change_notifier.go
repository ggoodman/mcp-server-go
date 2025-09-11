package mcpserver

import (
	"context"
	"sync"
)

type ChangeNotifier struct {
	subscribers   []chan struct{}
	subscribersMu sync.RWMutex
	closed        bool
}

// Notify triggers a notification to all registered listeners that a set of resources
// has been invalidated or has changed.
func (cn *ChangeNotifier) Notify(ctx context.Context) error {
	cn.subscribersMu.RLock()
	defer cn.subscribersMu.RUnlock()

	if cn.closed {
		return nil
	}

	// Best-effort fan-out: non-blocking send to each subscriber to avoid
	// head-of-line blocking on slow consumers.
	for _, ch := range cn.subscribers {
		select {
		case ch <- struct{}{}:
			// delivered
		default:
			// drop if subscriber is backed up
		}
	}
	return nil
}

func (cn *ChangeNotifier) Close() {
	// Take exclusive lock so that no Notify holds a read lock while we swap/close.
	cn.subscribersMu.Lock()
	if cn.closed {
		cn.subscribersMu.Unlock()
		return
	}
	cn.closed = true
	subs := cn.subscribers
	// Clear internal state before releasing lock.
	cn.subscribers = nil
	cn.subscribersMu.Unlock()

	for _, ch := range subs {
		close(ch)
	}
}

type ChangeSubscriber interface {
	Subscriber() <-chan struct{}
}

func (cn *ChangeNotifier) Subscriber() <-chan struct{} {
	cn.subscribersMu.Lock()
	defer cn.subscribersMu.Unlock()

	if cn.closed {
		// Return a closed channel to indicate no further notifications.
		ch := make(chan struct{})
		close(ch)
		return ch
	}

	if cn.subscribers == nil {
		// Start with empty slice; capacity 1 to avoid immediate realloc.
		cn.subscribers = make([]chan struct{}, 0, 1)
	}

	// Buffered to avoid blocking Notify; we use non-blocking sends anyway.
	ch := make(chan struct{}, 1)
	cn.subscribers = append(cn.subscribers, ch)

	return ch
}
