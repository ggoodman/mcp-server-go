package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcp"
)

// Transport abstracts how requests/notifications are sent and allows transports
// to subscribe to response rendezvous channels before sending to avoid races.
type Transport interface {
	// SendRequest sends the request with the pre-allocated id. Implementations
	// may subscribe to the rendezvous topic (e.g., rv:<id>) before actually
	// emitting the request to guarantee no response is missed.
	SendRequest(ctx context.Context, id *jsonrpc.RequestID, req *jsonrpc.Request) error
	// SendCancelled emits a notifications/cancelled for the given id string.
	SendCancelled(ctx context.Context, requestID string) error
}

var (
	// ErrDispatcherClosed indicates the dispatcher is closed.
	ErrDispatcherClosed = errors.New("dispatcher closed")
	// ErrRemoteCancelled indicates the peer cancelled the request.
	ErrRemoteCancelled = errors.New("remote cancelled")
)

type pendingCall struct {
	respCh chan *jsonrpc.Response
	errCh  chan error
}

// Dispatcher coordinates server-initiated JSON-RPC requests with correlation,
// cancellation, and response routing. It is transport-agnostic.
type Dispatcher struct {
	t Transport

	mu      sync.Mutex
	pending map[string]*pendingCall // id.String() -> call

	nextID uint64

	closed   atomic.Bool
	closeErr error
}

// New constructs a Dispatcher using the provided transport.
func New(t Transport) *Dispatcher {
	return &Dispatcher{t: t, pending: make(map[string]*pendingCall)}
}

// Call sends a JSON-RPC request and waits for a response or context cancellation.
func (d *Dispatcher) Call(ctx context.Context, method string, params any) (*jsonrpc.Response, error) {
	if d.closed.Load() {
		if d.closeErr != nil {
			return nil, d.closeErr
		}
		return nil, ErrDispatcherClosed
	}

	// Allocate ID
	idNum := atomic.AddUint64(&d.nextID, 1)
	id := jsonrpc.NewRequestID(idNum)
	key := id.String()

	// Marshal params
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		paramsRaw = b
	}

	// Register pending
	pc := &pendingCall{respCh: make(chan *jsonrpc.Response, 1), errCh: make(chan error, 1)}
	d.mu.Lock()
	if d.closed.Load() {
		d.mu.Unlock()
		if d.closeErr != nil {
			return nil, d.closeErr
		}
		return nil, ErrDispatcherClosed
	}
	d.pending[key] = pc
	d.mu.Unlock()

	// Send request via transport. Transport may subscribe before emit.
	req := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: method, Params: paramsRaw, ID: id}
	if err := d.t.SendRequest(ctx, id, req); err != nil {
		d.mu.Lock()
		delete(d.pending, key)
		d.mu.Unlock()
		return nil, err
	}

	// Await response or cancellation
	select {
	case resp := <-pc.respCh:
		return resp, nil
	case err := <-pc.errCh:
		if err != nil {
			return nil, err
		}
		return nil, ErrDispatcherClosed
	case <-ctx.Done():
		// Best-effort cancel message to client via transport
		_ = d.t.SendCancelled(context.Background(), key)
		d.mu.Lock()
		delete(d.pending, key)
		d.mu.Unlock()
		return nil, ctx.Err()
	}
}

// OnResponse delivers an incoming response to a waiting call. Unmatched responses are ignored.
func (d *Dispatcher) OnResponse(resp *jsonrpc.Response) {
	if resp == nil || resp.ID == nil {
		return
	}
	key := resp.ID.String()
	d.mu.Lock()
	pc, ok := d.pending[key]
	if ok {
		delete(d.pending, key)
	}
	d.mu.Unlock()
	if ok {
		pc.respCh <- resp
	}
}

// OnNotification processes peer notifications relevant to outbound calls (cancel/progress).
func (d *Dispatcher) OnNotification(any jsonrpc.AnyMessage) {
	switch any.Method {
	case string(mcp.CancelledNotificationMethod):
		var p mcp.CancelledNotification
		if err := json.Unmarshal(any.Params, &p); err != nil {
			return
		}
		key := p.RequestID
		d.mu.Lock()
		pc, ok := d.pending[key]
		if ok {
			delete(d.pending, key)
		}
		d.mu.Unlock()
		if ok {
			pc.errCh <- ErrRemoteCancelled
		}
	case string(mcp.ProgressNotificationMethod):
		// Currently ignored; kept for forward compatibility.
		return
	}
}

// Close cancels all pending calls with the provided error and prevents new calls.
func (d *Dispatcher) Close(err error) {
	if !d.closed.CompareAndSwap(false, true) {
		return
	}
	if err == nil {
		err = ErrDispatcherClosed
	}
	d.closeErr = err
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, pc := range d.pending {
		delete(d.pending, key)
		pc.errCh <- err
	}
}
