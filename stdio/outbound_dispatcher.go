package stdio

import (
	"context"
	"encoding/json"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/internal/outbound"
)

// jsonrpcWriter is the minimal writer contract backed by writeMux.
type jsonrpcWriter interface{ writeJSONRPC(v any) error }

// stdioTransport implements outbound.Transport over writeMux.
type stdioTransport struct{ w jsonrpcWriter }

func (t stdioTransport) SendRequest(ctx context.Context, id *jsonrpc.RequestID, req *jsonrpc.Request) error {
	return t.w.writeJSONRPC(req)
}
func (t stdioTransport) SendCancelled(ctx context.Context, requestID string) error {
	n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: "notifications/cancelled", Params: mustJSON(map[string]any{"requestId": requestID})}
	return t.w.writeJSONRPC(n)
}

// outboundDispatcher wraps the internal dispatcher to preserve tests and API.
type outboundDispatcher struct{ d *outbound.Dispatcher }

func newOutboundDispatcher(w jsonrpcWriter) *outboundDispatcher {
	return &outboundDispatcher{d: outbound.New(stdioTransport{w: w})}
}

func (d *outboundDispatcher) Call(ctx context.Context, method string, params any) (*jsonrpc.Response, error) {
	return d.d.Call(ctx, method, params)
}
func (d *outboundDispatcher) OnResponse(resp *jsonrpc.Response)     { d.d.OnResponse(resp) }
func (d *outboundDispatcher) OnNotification(any jsonrpc.AnyMessage) { d.d.OnNotification(any) }
func (d *outboundDispatcher) Close(err error)                       { d.d.Close(err) }

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// Re-export common errors for compatibility with existing tests/consumers.
var (
	ErrDispatcherClosed = outbound.ErrDispatcherClosed
	ErrRemoteCancelled  = outbound.ErrRemoteCancelled
)
