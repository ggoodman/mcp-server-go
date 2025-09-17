package stdio

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

// jsonrpcWriter is the minimal writer contract backed by writeMux.
type jsonrpcWriter interface {
	writeJSONRPC(v any) error
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

// outboundDispatcher coordinates server-initiated JSON-RPC requests over stdio.
type outboundDispatcher struct {
	w jsonrpcWriter

	mu      sync.Mutex
	pending map[string]*pendingCall // id.String() -> call

	nextID uint64

	closed   atomic.Bool
	closeErr error
}

func newOutboundDispatcher(w jsonrpcWriter) *outboundDispatcher {
	return &outboundDispatcher{
		w:       w,
		pending: make(map[string]*pendingCall),
	}
}

// Call sends a JSON-RPC request and waits for a response or context cancellation.
func (d *outboundDispatcher) Call(ctx context.Context, method string, params any) (*jsonrpc.Response, error) {
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

	// Send request
	req := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: method, Params: paramsRaw, ID: id}
	if err := d.w.writeJSONRPC(req); err != nil {
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
		// Best-effort cancel message to client
		_ = d.w.writeJSONRPC(&jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.CancelledNotificationMethod),
			Params:         mustJSON(mcp.CancelledNotification{RequestID: key}),
		})
		d.mu.Lock()
		delete(d.pending, key)
		d.mu.Unlock()
		return nil, ctx.Err()
	}
}

// OnResponse delivers an incoming response to a waiting call. Unmatched responses are ignored.
func (d *outboundDispatcher) OnResponse(resp *jsonrpc.Response) {
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
func (d *outboundDispatcher) OnNotification(any jsonrpc.AnyMessage) {
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
func (d *outboundDispatcher) Close(err error) {
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

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
