package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-streaming-http-go/mcp"
	"github.com/google/uuid"
)

// SessionOption is a functional option for configuring a session created
// by the SessionManager. These options can be used to enable or disable
// specific capabilities for the session.
type SessionOption func(*session)

func WithSamplingCapability() SessionOption {
	return func(s *session) {
		s.sampling = &samplingCapabilityImpl{sess: s}
	}
}

func WithRootsCapability(supportsListChanged bool) SessionOption {
	return func(s *session) {
		s.roots = &rootsCapabilityImpl{sess: s, supportsListChanged: supportsListChanged}
	}
}

func WithElicitationCapability() SessionOption {
	return func(s *session) {
		s.elicitation = &elicitationCapabilityImpl{sess: s}
	}
}

// Private concrete implementations for capabilities

type samplingCapabilityImpl struct {
	sess *session
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
	sess                *session
	supportsListChanged bool
}

func (r *rootsCapabilityImpl) ListRoots(ctx context.Context) (*mcp.ListRootsResult, error) {
	var out mcp.ListRootsResult
	if err := rpcCallToClient(ctx, r.sess, mcp.RootsListMethod, &mcp.ListRootsRequest{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *rootsCapabilityImpl) RegisterRootsListChangedListener(ctx context.Context, listener RootsListChangedListener) (supported bool, err error) {
	if !r.supportsListChanged {
		return false, nil
	}

	return false, errors.New("not implemented")
}

type elicitationCapabilityImpl struct {
	sess *session
}

func (e *elicitationCapabilityImpl) Elicit(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	var out mcp.ElicitResult
	if err := rpcCallToClient(ctx, e.sess, mcp.ElicitationCreateMethod, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// rpcCallToClient sends a JSON-RPC request to the client via the session's
// message stream and awaits the corresponding response using the host rendezvous.
func rpcCallToClient[T any](ctx context.Context, sess *session, method mcp.Method, params any, out *T) error {
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
	ttl := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d > 0 {
			ttl = d
		}
	}

	// Register waiter before publishing the request
	awaiter, err := sess.backend.BeginAwait(ctx, sess.id, corr, ttl)
	if err != nil {
		return fmt.Errorf("begin await: %w", err)
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
		_ = awaiter.Cancel(context.Background())
		return fmt.Errorf("marshal request: %w", err)
	}
	if _, err := sess.backend.PublishSession(ctx, sess.id, reqBytes); err != nil {
		_ = awaiter.Cancel(context.Background())
		return fmt.Errorf("publish request: %w", err)
	}

	// Await response bytes
	respBytes, err := awaiter.Recv(ctx)
	if err != nil {
		// If the await was canceled (TTL fired) and the call had a deadline,
		// surface a context-style timeout for clearer semantics, regardless of
		// minor scheduling races between the timer and ctx cancellation.
		if errors.Is(err, ErrAwaitCanceled) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if _, ok := ctx.Deadline(); ok {
				return context.DeadlineExceeded
			}
		}
		return err
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
