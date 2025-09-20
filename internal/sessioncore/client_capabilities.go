package sessioncore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/internal/validation"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/google/uuid"
)

// Private concrete implementations for capabilities

// simpleSession is a minimal adapter the capability impls use to reach the host.
type SimpleSession struct {
	id      string
	backend sessions.SessionHost
}

type samplingCapabilityImpl struct {
	sess *SimpleSession
}

func (s *samplingCapabilityImpl) CreateMessage(ctx context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	var out mcp.CreateMessageResult
	if err := rpcCallToClient(ctx, s.sess, mcp.SamplingCreateMessageMethod, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type rootsCapabilityImpl struct {
	sess                *SimpleSession
	supportsListChanged bool
}

func (r *rootsCapabilityImpl) ListRoots(ctx context.Context) (*mcp.ListRootsResult, error) {
	var out mcp.ListRootsResult
	if err := rpcCallToClient(ctx, r.sess, mcp.RootsListMethod, &mcp.ListRootsRequest{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *rootsCapabilityImpl) RegisterRootsListChangedListener(ctx context.Context, listener sessions.RootsListChangedListener) (supported bool, err error) {
	if !r.supportsListChanged {
		return false, nil
	}
	topic := string(mcp.RootsListChangedNotificationMethod)
	_, err = r.sess.backend.SubscribeEvents(ctx, r.sess.id, topic, func(ctx context.Context, _ []byte) error {
		return listener(ctx)
	})
	if err != nil {
		return true, err
	}
	return true, nil
}

type elicitationCapabilityImpl struct {
	sess *SimpleSession
}

func (e *elicitationCapabilityImpl) Elicit(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	// Internal validation of schema before sending to client to catch server-side errors early.
	if err := validation.ElicitationSchema(&req.RequestedSchema); err != nil {
		return nil, err
	}
	var out mcp.ElicitResult
	if err := rpcCallToClient(ctx, e.sess, mcp.ElicitationCreateMethod, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// (validation logic unified in internal/validation)

// rpcCallToClient sends a JSON-RPC request to the client via the session's
// message stream and awaits the corresponding response using the host rendezvous.
func rpcCallToClient[T any](ctx context.Context, sess *SimpleSession, method mcp.Method, params any, out *T) error {
	if sess == nil || sess.backend == nil {
		return fmt.Errorf("session backend unavailable")
	}

	// Marshal params
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params: %w", err)
		}
		paramsRaw = b
	}

	// Create correlation id and derive TTL from ctx
	corr := uuid.NewString()
	// TTL is governed by ctx deadline; no separate timer is required for pub/sub

	// Prepare rendezvous subscription before publishing the request
	rvTopic := "rv:" + corr
	recvCh := make(chan []byte, 1)
	// Use a child context to be able to unsubscribe independently on error paths
	subCtx, cancelSub := context.WithCancel(ctx)
	_, err := sess.backend.SubscribeEvents(subCtx, sess.id, rvTopic, func(ctx context.Context, payload []byte) error {
		select {
		case recvCh <- append([]byte(nil), payload...):
		default:
		}
		// one-shot; cancel subscription after first delivery
		cancelSub()
		return nil
	})
	if err != nil {
		cancelSub()
		return fmt.Errorf("subscribe rendezvous: %w", err)
	}

	// Build and publish the request
	req := &jsonrpc.Request{
		JSONRPCVersion: jsonrpc.ProtocolVersion,
		Method:         string(method),
		Params:         paramsRaw,
		ID:             jsonrpc.NewRequestID(corr),
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		cancelSub()
		return fmt.Errorf("marshal request: %w", err)
	}
	if _, err := sess.backend.PublishSession(ctx, sess.id, reqBytes); err != nil {
		cancelSub()
		return fmt.Errorf("publish request: %w", err)
	}

	// Await response bytes
	var respBytes []byte
	select {
	case <-ctx.Done():
		cancelSub()
		if _, ok := ctx.Deadline(); ok {
			return context.DeadlineExceeded
		}
		return ctx.Err()
	case b := <-recvCh:
		respBytes = b
	}

	var resp jsonrpc.Response
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("json-rpc error (%d): %s", resp.Error.Code, resp.Error.Message)
	}
	if len(resp.Result) == 0 {
		return fmt.Errorf("empty result")
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("decode result: %w", err)
	}
	return nil
}
