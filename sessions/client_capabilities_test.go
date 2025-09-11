package sessions_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-streaming-http-go/mcp"
	"github.com/ggoodman/mcp-streaming-http-go/sessions"
)

// simpleHost is a minimal in-test sessions.SessionHost used to validate
// capability RPCs without importing concrete hosts to avoid cycles.
type simpleHost struct {
	msgs    chan []byte
	awaitCh map[string]chan []byte
}

func newSimpleHost() *simpleHost {
	return &simpleHost{msgs: make(chan []byte, 10), awaitCh: make(map[string]chan []byte)}
}

func (h *simpleHost) PublishSession(ctx context.Context, sessionID string, data []byte) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case h.msgs <- append([]byte(nil), data...):
		return "1", nil
	}
}

func (h *simpleHost) SubscribeSession(ctx context.Context, sessionID string, lastEventID string, handler sessions.MessageHandlerFunction) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case b := <-h.msgs:
			if err := handler(ctx, "1", b); err != nil {
				return err
			}
		}
	}
}

func (h *simpleHost) CleanupSession(ctx context.Context, sessionID string) error { return nil }
func (h *simpleHost) AddRevocation(ctx context.Context, sessionID string, ttl time.Duration) error {
	return nil
}
func (h *simpleHost) IsRevoked(ctx context.Context, sessionID string) (bool, error) {
	return false, nil
}
func (h *simpleHost) BumpEpoch(ctx context.Context, scope sessions.RevocationScope) (int64, error) {
	return 0, nil
}
func (h *simpleHost) GetEpoch(ctx context.Context, scope sessions.RevocationScope) (int64, error) {
	return 0, nil
}

type shAwaiter struct {
	h   *simpleHost
	key string
}

func (a *shAwaiter) Recv(ctx context.Context) ([]byte, error) {
	ch := a.h.awaitCh[a.key]
	select {
	case <-ctx.Done():
		_ = a.Cancel(context.Background())
		return nil, ctx.Err()
	case b, ok := <-ch:
		if !ok {
			// Check if context is done to return the proper error
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				return nil, sessions.ErrAwaitCanceled
			}
		}
		return b, nil
	}
}
func (a *shAwaiter) Cancel(ctx context.Context) error {
	if ch, ok := a.h.awaitCh[a.key]; ok {
		close(ch)
		delete(a.h.awaitCh, a.key)
	}
	return nil
}

func (h *simpleHost) BeginAwait(ctx context.Context, sessionID, correlationID string, ttl time.Duration) (sessions.Awaiter, error) {
	key := sessionID + "|" + correlationID
	if _, ok := h.awaitCh[key]; ok {
		return nil, sessions.ErrAwaitExists
	}
	ch := make(chan []byte, 1)
	h.awaitCh[key] = ch
	// TTL cleanup
	if ttl > 0 {
		tmr := time.NewTimer(ttl)
		go func() {
			select {
			case <-ctx.Done():
			case <-tmr.C:
				if c, ok := h.awaitCh[key]; ok {
					close(c)
					delete(h.awaitCh, key)
				}
			}
		}()
	}
	return &shAwaiter{h: h, key: key}, nil
}

func (h *simpleHost) Fulfill(ctx context.Context, sessionID, correlationID string, data []byte) (bool, error) {
	key := sessionID + "|" + correlationID
	ch, ok := h.awaitCh[key]
	if !ok {
		return false, nil
	}
	select {
	case ch <- append([]byte(nil), data...):
		close(ch)
		delete(h.awaitCh, key)
		return true, nil
	default:
		close(ch)
		delete(h.awaitCh, key)
		return false, nil
	}
}

// Test rpcCallToClient happy path: request is observed on the session stream and fulfilled via rendezvous.
func TestRPCCallToClient_Success(t *testing.T) {
	host := newSimpleHost()
	mgr := sessions.NewManager(host)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Subscriber reads the request and fulfills via rendezvous with a result
	go func() {
		// We don't yet know the session id; subscribe when created below.
		// To keep it simple, we subscribe after session creation.
	}()

	// Create a session with sampling capability
	sess, err := mgr.CreateSession(ctx, "u-1", sessions.WithSamplingCapability())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	go func() {
		_ = host.SubscribeSession(ctx, sess.SessionID(), "", func(ctx context.Context, msgID string, msg []byte) error {
			var req jsonrpc.Request
			if err := json.Unmarshal(msg, &req); err != nil {
				return err
			}
			// Build a minimal valid result for sampling/createMessage
			result := &mcp.CreateMessageResult{Role: mcp.RoleAssistant, Content: []mcp.ContentBlock{}, Model: "test-model"}
			resp, err := jsonrpc.NewResultResponse(req.ID, result)
			if err != nil {
				return err
			}
			b, _ := json.Marshal(resp)
			_, _ = host.Fulfill(ctx, sess.SessionID(), req.ID.String(), b)
			return nil
		})
	}()

	// Issue the client RPC
	cap, ok := sess.GetSamplingCapability()
	if !ok {
		t.Fatalf("sampling capability not available")
	}
	out, err := cap.CreateMessage(ctx, &mcp.CreateMessageRequest{Messages: []mcp.SamplingMessage{}})
	if err != nil {
		t.Fatalf("rpcCallToClient failed: %v", err)
	}
	if out.Model != "test-model" || out.Role != mcp.RoleAssistant {
		t.Fatalf("unexpected result: %+v", out)
	}
}

// Test timeout: no subscriber fulfills; await should return ctx error.
func TestRPCCallToClient_Timeout(t *testing.T) {
	host := newSimpleHost()
	mgr := sessions.NewManager(host)
	sess, err := mgr.CreateSession(context.Background(), "u-2", sessions.WithSamplingCapability())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	cap, ok := sess.GetSamplingCapability()
	if !ok {
		t.Fatalf("sampling capability not available")
	}
	_, err = cap.CreateMessage(ctx, &mcp.CreateMessageRequest{Messages: []mcp.SamplingMessage{}})
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context error, got %v", err)
	}
}

// Test JSON-RPC error response mapping to Go error.
func TestRPCCallToClient_ErrorResponse(t *testing.T) {
	host := newSimpleHost()
	mgr := sessions.NewManager(host)
	sess, err := mgr.CreateSession(context.Background(), "u-3", sessions.WithSamplingCapability())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = host.SubscribeSession(ctx, sess.SessionID(), "", func(ctx context.Context, msgID string, msg []byte) error {
			var req jsonrpc.Request
			if err := json.Unmarshal(msg, &req); err != nil {
				return err
			}
			resp := jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "bad params", nil)
			b, _ := json.Marshal(resp)
			_, _ = host.Fulfill(ctx, sess.SessionID(), req.ID.String(), b)
			return nil
		})
	}()

	cap, ok := sess.GetSamplingCapability()
	if !ok {
		t.Fatalf("sampling capability not available")
	}
	_, err = cap.CreateMessage(ctx, &mcp.CreateMessageRequest{Messages: []mcp.SamplingMessage{}})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

// Roots capability: success path
func TestRoots_List_Success(t *testing.T) {
	host := newSimpleHost()
	mgr := sessions.NewManager(host)
	sess, err := mgr.CreateSession(context.Background(), "u-4", sessions.WithRootsCapability(false))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = host.SubscribeSession(ctx, sess.SessionID(), "", func(ctx context.Context, msgID string, msg []byte) error {
			var req jsonrpc.Request
			if err := json.Unmarshal(msg, &req); err != nil {
				return err
			}
			if req.Method != string(mcp.RootsListMethod) {
				return nil
			}
			result := &mcp.ListRootsResult{Roots: []mcp.Root{{URI: "root://a"}, {URI: "root://b"}}}
			resp, err := jsonrpc.NewResultResponse(req.ID, result)
			if err != nil {
				return err
			}
			b, _ := json.Marshal(resp)
			_, _ = host.Fulfill(ctx, sess.SessionID(), req.ID.String(), b)
			return nil
		})
	}()

	cap, ok := sess.GetRootsCapability()
	if !ok {
		t.Fatalf("roots capability not available")
	}
	out, err := cap.ListRoots(ctx)
	if err != nil {
		t.Fatalf("ListRoots failed: %v", err)
	}
	if len(out.Roots) != 2 || out.Roots[0].URI != "root://a" {
		t.Fatalf("unexpected roots: %+v", out.Roots)
	}
}

// Roots capability: error response
func TestRoots_List_ErrorResponse(t *testing.T) {
	host := newSimpleHost()
	mgr := sessions.NewManager(host)
	sess, err := mgr.CreateSession(context.Background(), "u-5", sessions.WithRootsCapability(false))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = host.SubscribeSession(ctx, sess.SessionID(), "", func(ctx context.Context, msgID string, msg []byte) error {
			var req jsonrpc.Request
			if err := json.Unmarshal(msg, &req); err != nil {
				return err
			}
			if req.Method != string(mcp.RootsListMethod) {
				return nil
			}
			resp := jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "bad", nil)
			b, _ := json.Marshal(resp)
			_, _ = host.Fulfill(ctx, sess.SessionID(), req.ID.String(), b)
			return nil
		})
	}()

	cap, ok := sess.GetRootsCapability()
	if !ok {
		t.Fatalf("roots capability not available")
	}
	_, err = cap.ListRoots(ctx)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

// Elicitation capability: success path
func TestElicit_Success(t *testing.T) {
	host := newSimpleHost()
	mgr := sessions.NewManager(host)
	sess, err := mgr.CreateSession(context.Background(), "u-6", sessions.WithElicitationCapability())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = host.SubscribeSession(ctx, sess.SessionID(), "", func(ctx context.Context, msgID string, msg []byte) error {
			var req jsonrpc.Request
			if err := json.Unmarshal(msg, &req); err != nil {
				return err
			}
			if req.Method != string(mcp.ElicitationCreateMethod) {
				return nil
			}
			result := &mcp.ElicitResult{Values: map[string]any{"name": "Alice"}}
			resp, err := jsonrpc.NewResultResponse(req.ID, result)
			if err != nil {
				return err
			}
			b, _ := json.Marshal(resp)
			_, _ = host.Fulfill(ctx, sess.SessionID(), req.ID.String(), b)
			return nil
		})
	}()

	cap, ok := sess.GetElicitationCapability()
	if !ok {
		t.Fatalf("elicitation capability not available")
	}
	out, err := cap.Elicit(ctx, &mcp.ElicitRequest{Prompt: "Name?", Schema: mcp.ElicitationSchema{Type: "object", Properties: map[string]mcp.PrimitiveSchemaDefinition{"name": {Type: "string"}}}})
	if err != nil {
		t.Fatalf("Elicit failed: %v", err)
	}
	if out.Values["name"] != "Alice" {
		t.Fatalf("unexpected values: %+v", out.Values)
	}
}

// Elicitation capability: error response
func TestElicit_ErrorResponse(t *testing.T) {
	host := newSimpleHost()
	mgr := sessions.NewManager(host)
	sess, err := mgr.CreateSession(context.Background(), "u-7", sessions.WithElicitationCapability())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = host.SubscribeSession(ctx, sess.SessionID(), "", func(ctx context.Context, msgID string, msg []byte) error {
			var req jsonrpc.Request
			if err := json.Unmarshal(msg, &req); err != nil {
				return err
			}
			if req.Method != string(mcp.ElicitationCreateMethod) {
				return nil
			}
			resp := jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "bad", nil)
			b, _ := json.Marshal(resp)
			_, _ = host.Fulfill(ctx, sess.SessionID(), req.ID.String(), b)
			return nil
		})
	}()

	cap, ok := sess.GetElicitationCapability()
	if !ok {
		t.Fatalf("elicitation capability not available")
	}
	_, err = cap.Elicit(ctx, &mcp.ElicitRequest{Prompt: "Name?", Schema: mcp.ElicitationSchema{Type: "object", Properties: map[string]mcp.PrimitiveSchemaDefinition{"name": {Type: "string"}}}})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}
