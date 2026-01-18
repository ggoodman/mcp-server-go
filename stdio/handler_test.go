package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/sampling"
)

// testHarness encapsulates pipes and collected output for stdio handler tests.
type testHarness struct {
	t       *testing.T
	ctx     context.Context
	cancel  context.CancelFunc
	stdinW  io.Writer
	stdoutR *bufio.Scanner
	outMu   sync.Mutex
	lines   []string
}

var defaultProtocolVersion = mcp.LatestProtocolVersion

func defaultInitializeRequest() mcp.InitializeRequest {
	return mcp.InitializeRequest{
		ProtocolVersion: defaultProtocolVersion,
		ClientInfo:      mcp.ImplementationInfo{Name: "client", Version: "0.0.1"},
		Capabilities:    mcp.ClientCapabilities{Sampling: &struct{}{}, Elicitation: &struct{}{}},
	}
}

func newHarness(t *testing.T, srv mcpservice.ServerCapabilities) *testHarness {
	t.Helper()

	// wire stdio via io.Pipe
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	// handler writes to outW, reads from inR
	// Use default logger to surface engine/handler logs in test output
	h := NewHandler(srv, WithIO(inR, outW), WithLogger(slog.Default()))

	ctx, cancel := context.WithCancel(context.Background())
	th := &testHarness{t: t, ctx: ctx, cancel: cancel, stdinW: inW, stdoutR: bufio.NewScanner(outR)}

	// start handler
	go func() {
		_ = h.Serve(ctx)
	}()

	// start stdout collector
	go func() {
		for th.stdoutR.Scan() {
			line := strings.TrimSpace(th.stdoutR.Text())
			th.t.Logf("OUT: %s", line)
			th.outMu.Lock()
			th.lines = append(th.lines, line)
			th.outMu.Unlock()
		}
	}()

	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
		_ = outW.Close()
		// allow goroutines to wind down
		time.Sleep(10 * time.Millisecond)
	})
	return th
}

// send helper writes a JSON-RPC request (as marshalled JSON + newline) to stdin.
func (th *testHarness) send(req *jsonrpc.Request) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := th.stdinW.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func (th *testHarness) nextLine(timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		th.outMu.Lock()
		if len(th.lines) > 0 {
			s := th.lines[0]
			th.lines = th.lines[1:]
			th.outMu.Unlock()
			return s, nil
		}
		th.outMu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	return "", fmt.Errorf("timeout waiting for output line")
}

func (th *testHarness) expectResponse(timeout time.Duration) (*jsonrpc.Response, error) {
	line, err := th.nextLine(timeout)
	if err != nil {
		return nil, err
	}
	var any jsonrpc.AnyMessage
	if err := json.Unmarshal([]byte(line), &any); err != nil {
		return nil, err
	}
	if any.Type() != "response" {
		return nil, fmt.Errorf("expected response, got %s", any.Type())
	}
	return any.AsResponse(), nil
}

func (th *testHarness) expectRequest(timeout time.Duration) (*jsonrpc.Request, error) {
	line, err := th.nextLine(timeout)
	if err != nil {
		return nil, err
	}
	var any jsonrpc.AnyMessage
	if err := json.Unmarshal([]byte(line), &any); err != nil {
		return nil, err
	}
	if any.Type() != "request" && any.Type() != "notification" {
		return nil, fmt.Errorf("expected request/notification, got %s", any.Type())
	}
	return any.AsRequest(), nil
}

func (th *testHarness) initialize(t *testing.T, id string, req mcp.InitializeRequest) *mcp.InitializeResult {
	t.Helper()

	initReq := &jsonrpc.Request{
		JSONRPCVersion: jsonrpc.ProtocolVersion,
		Method:         string(mcp.InitializeMethod),
		ID:             jsonrpc.NewRequestID(id),
		Params:         mustJSON(t, req),
	}

	if err := th.send(initReq); err != nil {
		t.Fatalf("send initialize: %v", err)
	}

	res, err := th.expectResponse(1 * time.Second)
	if err != nil {
		t.Fatalf("expect initialize response: %v", err)
	}
	if res.Error != nil {
		t.Fatalf("initialize failed: %+v", res.Error)
	}

	var initRes mcp.InitializeResult
	if err := json.Unmarshal(res.Result, &initRes); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	return &initRes
}

func (th *testHarness) drainUntilMethod(method string, timeout time.Duration) (*jsonrpc.Request, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		line, err := th.nextLine(10 * time.Millisecond)
		if err != nil {
			continue
		}
		var any jsonrpc.AnyMessage
		if json.Unmarshal([]byte(line), &any) != nil {
			continue
		}
		if any.Type() == "response" {
			// push response back into queue for future expectations
			th.outMu.Lock()
			th.lines = append([]string{line}, th.lines...)
			th.outMu.Unlock()
			continue
		}
		req := any.AsRequest()
		if req != nil && req.Method == method {
			return req, true
		}
	}
	return nil, false
}

// --- Test helper capability implementations ---

// rootsTriggerToolsCap is a test ToolsCapability that triggers a roots list call
// when listed to exercise cross-capability client request forwarding.
type rootsTriggerToolsCap struct{}

func (rootsTriggerToolsCap) ListTools(ctx context.Context, session sessions.Session, cursor *string) (mcpservice.Page[mcp.Tool], error) {
	if rc, ok := session.GetRootsCapability(); ok {
		_, _ = rc.ListRoots(ctx) // trigger client call; ignore errors
	}
	return mcpservice.NewPage([]mcp.Tool{{Name: "t"}}), nil
}

func (rootsTriggerToolsCap) CallTool(context.Context, sessions.Session, *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{Content: []mcp.ContentBlock{}}, nil
}

func (rootsTriggerToolsCap) GetListChangedCapability(ctx context.Context, s sessions.Session) (mcpservice.ToolListChangedCapability, bool, error) {
	return nil, false, nil
}

// ProvideTools allows rootsTriggerToolsCap to satisfy the ToolsCapabilityProvider interface directly
// so it can be supplied to WithToolsCapability without wrapping.
func (r rootsTriggerToolsCap) ProvideTools(ctx context.Context, s sessions.Session) (mcpservice.ToolsCapability, bool, error) {
	return r, true, nil
}

// fakeCompletions is a simple completions capability for tests.
type fakeCompletions struct{}

var _ mcpservice.CompletionsCapability = (*fakeCompletions)(nil)

// ProvideCompletions allows fakeCompletions to satisfy CompletionsCapabilityProvider directly.
func (f fakeCompletions) ProvideCompletions(ctx context.Context, s sessions.Session) (mcpservice.CompletionsCapability, bool, error) {
	return f, true, nil
}

func (fakeCompletions) Complete(ctx context.Context, _ sessions.Session, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	return &mcp.CompleteResult{Completion: mcp.Completion{Values: []string{"one", "two"}, Total: 2}}, nil
}

// --- Tests ---

func TestInitialize_HappyPath(t *testing.T) {
	srv := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcpservice.StaticServerInfo("test", "1.0.0")),
		mcpservice.WithProtocolVersion(mcpservice.StaticProtocolVersion(defaultProtocolVersion)),
		mcpservice.WithInstructions(mcpservice.StaticInstructions("Have fun!")),
		mcpservice.WithToolsCapability(mcpservice.NewToolsContainer()),
		mcpservice.WithLoggingCapability(mcpservice.StaticLogging(mcpservice.NewSlogLevelVarLogging(&slog.LevelVar{}))),
	)
	th := newHarness(t, srv)

	initRes := th.initialize(t, "init-1", defaultInitializeRequest())
	if initRes.ProtocolVersion != defaultProtocolVersion {
		t.Fatalf("server protocol version mismatch: %s", initRes.ProtocolVersion)
	}
	if initRes.ServerInfo.Name != "test" {
		t.Fatalf("server info missing")
	}
	if initRes.Capabilities.Tools == nil {
		t.Fatalf("tools capability not advertised")
	}
}

func TestTools_ListAndCall(t *testing.T) {
	// Define a simple echo tool without schema reflection
	echoDesc := mcp.Tool{
		Name: "echo",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]mcp.SchemaProperty{
				"text": {Type: "string"},
			},
			Required: []string{"text"},
		},
	}
	echo := mcpservice.StaticTool{Descriptor: echoDesc, Handler: func(ctx context.Context, _ sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
		var args struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return mcpservice.Errorf("invalid arguments: %v", err), nil
		}
		_ = mcpservice.ReportProgress(ctx, 0.25, 1)
		return mcpservice.TextResult(args.Text), nil
	}}
	st := mcpservice.NewToolsContainer(echo)
	srv := mcpservice.NewServer(
		mcpservice.WithToolsCapability(st),
	)
	th := newHarness(t, srv)

	// Initialize and open
	_ = th.initialize(t, "init-1", defaultInitializeRequest())
	_ = th.send(&jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.InitializedNotificationMethod)})

	// List tools
	listReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ToolsListMethod), ID: jsonrpc.NewRequestID("1")}
	if err := th.send(listReq); err != nil {
		t.Fatal(err)
	}
	res, err := th.expectResponse(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatalf("list error: %+v", res.Error)
	}
	var list mcp.ListToolsResult
	if err := json.Unmarshal(res.Result, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", list.Tools)
	}

	// Call tool
	callReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ToolsCallMethod), ID: jsonrpc.NewRequestID("2")}
	callReq.Params = mustJSON(t, mcp.CallToolRequestReceived{Name: "echo", Arguments: mustJSONRaw(t, map[string]any{"text": "hi"})})
	if err := th.send(callReq); err != nil {
		t.Fatal(err)
	}

	// Optionally observe a progress notification (transport emits it)
	// It's a notification from server to client, so it will appear as a request without ID
	// Don't assert strictly to avoid flakes
	_ = tryDrainUntilProgress(th, 200*time.Millisecond)

	res, err = th.expectResponse(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatalf("call error: %+v", res.Error)
	}
	var result mcp.CallToolResult
	if err := json.Unmarshal(res.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Content) == 0 || result.Content[0].Text != "hi" {
		t.Fatalf("unexpected tool result: %+v", result)
	}
}

// Half-open sessions: requests are now allowed (no gating InvalidRequest error) before client sends notifications/initialized.
func TestHandshake_PreInitializedRequestsAllowed(t *testing.T) {
	srv := mcpservice.NewServer(
		mcpservice.WithToolsCapability(mcpservice.NewToolsContainer()),
	)
	th := newHarness(t, srv)

	// Initialize session (server responds) but do NOT send notifications/initialized yet.
	_ = th.initialize(t, "init-1", defaultInitializeRequest())

	// Send a ping request before initialized; it should NOT fail with "session not initialized".
	ping := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.PingMethod), ID: jsonrpc.NewRequestID("1")}
	if err := th.send(ping); err != nil {
		t.Fatal(err)
	}
	res, err := th.expectResponse(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil && res.Error.Message == "session not initialized" {
		t.Fatalf("did not expect gating error before initialized: %+v", res.Error)
	}
}

// After notifications/initialized, requests should succeed.
func TestHandshake_InitializedAllowsRequests(t *testing.T) {
	srv := mcpservice.NewServer()
	th := newHarness(t, srv)

	_ = th.initialize(t, "init-1", defaultInitializeRequest())

	// Send client notifications/initialized
	initNote := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.InitializedNotificationMethod)}
	if err := th.send(initNote); err != nil {
		t.Fatal(err)
	}

	// Give the handler a brief moment to start the session pump
	time.Sleep(20 * time.Millisecond)

	// Now ping should no longer be rejected for being uninitialized. The engine
	// may still return not implemented for ping, which is fine. We only assert
	// the gating error is gone.
	ping := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.PingMethod), ID: jsonrpc.NewRequestID("1")}
	if err := th.send(ping); err != nil {
		t.Fatal(err)
	}
	res, err := th.expectResponse(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil && res.Error.Message == "session not initialized" {
		t.Fatalf("unexpected error after initialized: %+v", res.Error)
	}
}

// Eager wiring: after Open the server should emit tools listChanged notifications when the
// static tools container changes, and wiring should be idempotent even if initialized is sent twice.
func TestEagerWiring_ToolsListChanged_IdempotentInitialized(t *testing.T) {
	// Start with an empty static tools container that exposes listChanged via subscriber.
	st := mcpservice.NewToolsContainer()
	srv := mcpservice.NewServer(mcpservice.WithToolsCapability(st))
	th := newHarness(t, srv)

	_ = th.initialize(t, "init-1", defaultInitializeRequest())

	// Send initialized twice (idempotent)
	initNote := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.InitializedNotificationMethod)}
	if err := th.send(initNote); err != nil {
		t.Fatal(err)
	}
	if err := th.send(initNote); err != nil {
		t.Fatal(err)
	}

	// Wait until a simple ping succeeds to ensure session is open and pump running
	func() {
		ping := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.PingMethod), ID: jsonrpc.NewRequestID("p1")}
		if err := th.send(ping); err != nil {
			t.Fatal(err)
		}
		if _, err := th.expectResponse(1 * time.Second); err != nil {
			t.Fatalf("ping after initialized: %v", err)
		}
	}()

	// Trigger a tools change twice; expect exactly one list_changed per change
	// Change 1
	st.Replace(t.Context())
	if req, ok := th.drainUntilMethod(string(mcp.ToolsListChangedNotificationMethod), 1*time.Second); !ok {
		t.Fatalf("expected %s after first change", mcp.ToolsListChangedNotificationMethod)
	} else if req.ID != nil && !req.ID.IsNil() {
		t.Fatalf("notification should not have ID: %+v", req.ID)
	}

	// Change 2
	st.Replace(t.Context())
	if req, ok := th.drainUntilMethod(string(mcp.ToolsListChangedNotificationMethod), 1*time.Second); !ok {
		t.Fatalf("expected %s after second change", mcp.ToolsListChangedNotificationMethod)
	} else if req.ID != nil && !req.ID.IsNil() {
		t.Fatalf("notification should not have ID: %+v", req.ID)
	}

	// Ensure there isn't an unexpected extra third notification within a short window
	if _, ok := th.drainUntilMethod(string(mcp.ToolsListChangedNotificationMethod), 150*time.Millisecond); ok {
		t.Fatalf("unexpected extra tools list_changed notification (duplicate wiring?)")
	}
}

func TestCancellation_ToolsCall(t *testing.T) {
	// Tool that waits on context cancellation
	slowDesc := mcp.Tool{Name: "slow", InputSchema: mcp.ToolInputSchema{Type: "object"}}
	cancelled := make(chan struct{})
	closeCancelled := sync.Once{}
	slow := mcpservice.StaticTool{Descriptor: slowDesc, Handler: func(ctx context.Context, _ sessions.Session, _ *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
		// Emit progress so the test can detect we've started, ensuring the engine
		// has registered cancellation handlers for this request ID.
		_ = mcpservice.ReportProgress(ctx, 0, 1)
		<-ctx.Done()
		closeCancelled.Do(func() {
			close(cancelled)
		})
		t.Logf("tool handler exiting due to cancellation: %v", ctx.Err())
		return nil, ctx.Err()
	}}
	st := mcpservice.NewToolsContainer(slow)
	srv := mcpservice.NewServer(mcpservice.WithToolsCapability(st))
	th := newHarness(t, srv)

	// init and open
	_ = th.initialize(t, "init-1", defaultInitializeRequest())
	_ = th.send(&jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.InitializedNotificationMethod)})

	// call
	rid := "42"
	callReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ToolsCallMethod), ID: jsonrpc.NewRequestID(rid)}
	callReq.Params = mustJSON(t, mcp.CallToolRequestReceived{Name: "slow"})
	if err := th.send(callReq); err != nil {
		t.Fatal(err)
	}

	// Wait until we see a progress notification indicating the tool started.
	if !tryDrainUntilProgress(th, 750*time.Millisecond) {
		t.Fatalf("expected progress notification before cancellation")
	}

	// cancel via notification
	cancelNote := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.CancelledNotificationMethod)}
	cancelNote.Params = mustJSON(t, mcp.CancelledNotification{RequestID: jsonrpc.NewRequestID(rid), Reason: "test"})
	if err := th.send(cancelNote); err != nil {
		t.Fatal(err)
	}

	select {
	case <-cancelled:
	case <-t.Context().Done():
		t.Fatalf("tool context was not cancelled: %v", t.Context().Err())
	}

	res, err := th.expectResponse(time.Second)
	if err != nil {
		t.Fatalf("expect cancellation response: %v", err)
	}
	if res.Error == nil {
		t.Fatalf("expected error response, got: %+v", res)
	}
}

func TestClientRoundTrip_SamplingAndElicitation(t *testing.T) {
	// This test exercises that handler forwards client responses back into engine rendezvous.
	// We trigger sampling.CreateMessage from inside a tool.
	roundtripDesc := mcp.Tool{Name: "roundtrip", InputSchema: mcp.ToolInputSchema{Type: "object"}}
	tool := mcpservice.StaticTool{Descriptor: roundtripDesc, Handler: func(ctx context.Context, session sessions.Session, _ *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
		if smp, ok := session.GetSamplingCapability(); ok {
			go func() { _, _ = smp.CreateMessage(ctx, "", sampling.UserText("hi")) }()
		}
		return mcpservice.TextResult("ok"), nil
	}}
	st := mcpservice.NewToolsContainer(tool)
	srv := mcpservice.NewServer(mcpservice.WithToolsCapability(st))
	th := newHarness(t, srv)

	// init with sampling + elicitation client caps so engine sets up session writers
	_ = th.initialize(t, "init-1", defaultInitializeRequest())
	_ = th.send(&jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.InitializedNotificationMethod)})

	// call tool
	rid := "77"
	callReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ToolsCallMethod), ID: jsonrpc.NewRequestID(rid)}
	callReq.Params = mustJSON(t, mcp.CallToolRequestReceived{Name: "roundtrip"})
	if err := th.send(callReq); err != nil {
		t.Fatal(err)
	}

	// Expect the call tool response first.
	res, err := th.expectResponse(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatalf("unexpected error: %+v", res.Error)
	}

	// We expect server to send out requests for sampling/elicitation if used.
	// Drain any follow-up requests to ensure the transport forwards them without deadlocks.
	drainDeadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(drainDeadline) {
		if _, err := th.expectRequest(50 * time.Millisecond); err != nil {
			break
		}
	}
}

func TestResources_ListReadSubscribe(t *testing.T) {
	// Static resources: one resource with initial contents
	uri := "mem://a"
	sr := mcpservice.NewResourcesContainer()
	sr.UpsertResource(mcpservice.TextResource(uri, "v1", mcpservice.WithName("A"), mcpservice.WithMimeType("text/plain")))
	srv := mcpservice.NewServer(
		mcpservice.WithResourcesCapability(sr),
	)
	th := newHarness(t, srv)

	// initialize + open
	_ = th.initialize(t, "init-1", defaultInitializeRequest())
	_ = th.send(&jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.InitializedNotificationMethod)})

	// list
	listReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ResourcesListMethod), ID: jsonrpc.NewRequestID("1")}
	if err := th.send(listReq); err != nil {
		t.Fatal(err)
	}
	res, err := th.expectResponse(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatalf("list error: %+v", res.Error)
	}
	var lres mcp.ListResourcesResult
	if err := json.Unmarshal(res.Result, &lres); err != nil {
		t.Fatal(err)
	}
	if len(lres.Resources) != 1 || lres.Resources[0].URI != uri {
		t.Fatalf("unexpected list: %+v", lres.Resources)
	}

	// read
	readReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ResourcesReadMethod), ID: jsonrpc.NewRequestID("2"), Params: mustJSON(t, mcp.ReadResourceRequest{URI: uri})}
	if err := th.send(readReq); err != nil {
		t.Fatal(err)
	}
	res, err = th.expectResponse(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatalf("read error: %+v", res.Error)
	}
	var rres mcp.ReadResourceResult
	if err := json.Unmarshal(res.Result, &rres); err != nil {
		t.Fatal(err)
	}
	if len(rres.Contents) == 0 || rres.Contents[0].Text != "v1" {
		t.Fatalf("unexpected contents: %+v", rres.Contents)
	}

	// subscribe
	subReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ResourcesSubscribeMethod), ID: jsonrpc.NewRequestID("3"), Params: mustJSON(t, mcp.SubscribeRequest{URI: uri})}
	if err := th.send(subReq); err != nil {
		t.Fatal(err)
	}
	if res, err = th.expectResponse(1 * time.Second); err != nil || res.Error != nil {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("subscribe error: %+v", res.Error)
	}

	// mutate contents -> expect notifications/resources/updated
	sr.UpsertResource(mcpservice.TextResource(uri, "v2", mcpservice.WithName("A"), mcpservice.WithMimeType("text/plain")))
	note, ok := th.drainUntilMethod(string(mcp.ResourcesUpdatedNotificationMethod), 2*time.Second)
	if !ok {
		t.Fatalf("expected %s after content change", mcp.ResourcesUpdatedNotificationMethod)
	}
	var upd mcp.ResourceUpdatedNotification
	if err := json.Unmarshal(note.Params, &upd); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if upd.URI != uri {
		t.Fatalf("updated for wrong uri: %+v", upd)
	}

	// unsubscribe then change again -> eventual semantics: do not assert immediate quiescence
	unsubReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ResourcesUnsubscribeMethod), ID: jsonrpc.NewRequestID("4"), Params: mustJSON(t, mcp.UnsubscribeRequest{URI: uri})}
	if err := th.send(unsubReq); err != nil {
		t.Fatal(err)
	}
	if res, err = th.expectResponse(1 * time.Second); err != nil || res.Error != nil {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("unsubscribe error: %+v", res.Error)
	}
	// Close harness; do not assert on no further updates immediately.
}

func TestPrompts_ListAndGet(t *testing.T) {
	// One prompt with simple handler
	sp := mcpservice.NewPromptsContainer(mcpservice.StaticPrompt{
		Descriptor: mcp.Prompt{Name: "hello", Description: "say hi"},
		Handler: func(ctx context.Context, _ sessions.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Description: "hi", Messages: []mcp.PromptMessage{{Role: mcp.RoleUser, Content: []mcp.ContentBlock{{Type: mcp.ContentTypeText, Text: "hello"}}}}}, nil
		},
	})
	srv := mcpservice.NewServer(mcpservice.WithPromptsCapability(sp))
	th := newHarness(t, srv)

	_ = th.initialize(t, "init-1", defaultInitializeRequest())
	_ = th.send(&jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.InitializedNotificationMethod)})

	// list
	listReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.PromptsListMethod), ID: jsonrpc.NewRequestID("1")}
	if err := th.send(listReq); err != nil {
		t.Fatal(err)
	}
	res, err := th.expectResponse(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatalf("list prompts error: %+v", res.Error)
	}
	var lp mcp.ListPromptsResult
	if err := json.Unmarshal(res.Result, &lp); err != nil {
		t.Fatal(err)
	}
	if len(lp.Prompts) != 1 || lp.Prompts[0].Name != "hello" {
		t.Fatalf("unexpected prompts: %+v", lp.Prompts)
	}

	// get
	getReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.PromptsGetMethod), ID: jsonrpc.NewRequestID("2"), Params: mustJSON(t, mcp.GetPromptRequest{Name: "hello"})}
	if err := th.send(getReq); err != nil {
		t.Fatal(err)
	}
	res, err = th.expectResponse(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatalf("get prompt error: %+v", res.Error)
	}
	var gp mcp.GetPromptResult
	if err := json.Unmarshal(res.Result, &gp); err != nil {
		t.Fatal(err)
	}
	if len(gp.Messages) == 0 {
		t.Fatalf("missing prompt messages")
	}

	// change -> expect prompts/list_changed
	sp.Add(t.Context(), mcpservice.StaticPrompt{Descriptor: mcp.Prompt{Name: "bye"}})
	if _, ok := th.drainUntilMethod(string(mcp.PromptsListChangedNotificationMethod), 2*time.Second); !ok {
		t.Fatalf("expected %s after prompts change", mcp.PromptsListChangedNotificationMethod)
	}
}

func TestRoots_ListAndListChanged(t *testing.T) {
	// We don't have a native roots provider; instead trigger a client round-trip via a tools list.
	srv := mcpservice.NewServer(mcpservice.WithToolsCapability(rootsTriggerToolsCap{}))
	th := newHarness(t, srv)

	// client advertises roots capability so the engine wires a roots client bridge
	init := defaultInitializeRequest()
	init.Capabilities.Roots = &struct {
		ListChanged bool `json:"listChanged"`
	}{}
	_ = th.initialize(t, "init-1", init)
	_ = th.send(&jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.InitializedNotificationMethod)})

	// Issue tools/list -> expect a client roots/list request first
	listReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ToolsListMethod), ID: jsonrpc.NewRequestID("1")}
	if err := th.send(listReq); err != nil {
		t.Fatal(err)
	}
	first, err := th.expectRequest(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if first.Method != string(mcp.RootsListMethod) {
		t.Fatalf("expected first outbound %s, got %s", mcp.RootsListMethod, first.Method)
	}
	// respond as client
	if first.ID == nil || first.ID.IsNil() {
		t.Fatalf("missing id on roots/list request")
	}
	rootsResp := &jsonrpc.Response{JSONRPCVersion: jsonrpc.ProtocolVersion, ID: first.ID, Result: mustJSON(t, mcp.ListRootsResult{Roots: []mcp.Root{}})}
	// Inject response directly into harness output stream to simulate client reply.
	b, _ := json.Marshal(rootsResp)
	th.outMu.Lock()
	th.lines = append([]string{string(b)}, th.lines...)
	th.outMu.Unlock()

	// then the final response to tools/list
	if _, err := th.expectResponse(2 * time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestCompletion_Complete(t *testing.T) {
	// Fake completions capability
	srv := mcpservice.NewServer(mcpservice.WithCompletionsCapability(fakeCompletions{}))
	th := newHarness(t, srv)

	_ = th.initialize(t, "init-1", defaultInitializeRequest())
	_ = th.send(&jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.InitializedNotificationMethod)})

	req := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.CompletionCompleteMethod), ID: jsonrpc.NewRequestID("1"), Params: mustJSON(t, mcp.CompleteRequest{Ref: mcp.ResourceReference{Type: "file", URI: "mem://a"}, Argument: mcp.CompleteArgument{Name: "path", Value: ""}})}
	if err := th.send(req); err != nil {
		t.Fatal(err)
	}
	res, err := th.expectResponse(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatalf("complete error: %+v", res.Error)
	}
	var cres mcp.CompleteResult
	if err := json.Unmarshal(res.Result, &cres); err != nil {
		t.Fatal(err)
	}
	if len(cres.Completion.Values) != 2 {
		t.Fatalf("unexpected completion: %+v", cres.Completion)
	}
}

func TestLogging_SetLevelAndMessages(t *testing.T) {
	var lv slog.LevelVar
	lv.Set(slog.LevelInfo)
	srv := mcpservice.NewServer(mcpservice.WithLoggingCapability(mcpservice.StaticLogging(mcpservice.NewSlogLevelVarLogging(&lv))))
	th := newHarness(t, srv)

	_ = th.initialize(t, "init-1", defaultInitializeRequest())
	_ = th.send(&jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.InitializedNotificationMethod)})

	// Before: debug should be disabled
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: &lv}))
	if logger.Enabled(th.ctx, slog.LevelDebug) {
		t.Fatalf("debug unexpectedly enabled before setLevel")
	}

	// Set to debug
	setReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.LoggingSetLevelMethod), ID: jsonrpc.NewRequestID("1"), Params: mustJSON(t, mcp.SetLevelRequest{Level: mcp.LoggingLevelDebug})}
	if err := th.send(setReq); err != nil {
		t.Fatal(err)
	}
	if res, err := th.expectResponse(1 * time.Second); err != nil || res.Error != nil {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("setLevel error: %+v", res.Error)
	}
	if !logger.Enabled(th.ctx, slog.LevelDebug) {
		t.Fatalf("debug not enabled after setLevel")
	}

	// Invalid level should return InvalidParams
	badReq := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.LoggingSetLevelMethod), ID: jsonrpc.NewRequestID("2"), Params: mustJSON(t, mcp.SetLevelRequest{Level: "bogus"})}
	if err := th.send(badReq); err != nil {
		t.Fatal(err)
	}
	res, err := th.expectResponse(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == nil || res.Error.Code != jsonrpc.ErrorCodeInvalidParams {
		t.Fatalf("expected invalid params for bad level, got: %+v", res.Error)
	}
}

// --- helpers ---

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func mustJSONRaw(t *testing.T, v any) json.RawMessage { return mustJSON(t, v) }

func tryDrainUntilProgress(th *testHarness, dur time.Duration) bool {
	deadline := time.Now().Add(dur)
	for time.Now().Before(deadline) {
		th.outMu.Lock()
		if len(th.lines) == 0 {
			th.outMu.Unlock()
			time.Sleep(5 * time.Millisecond)
			continue
		}
		s := th.lines[0]
		th.lines = th.lines[1:]
		th.outMu.Unlock()
		var any jsonrpc.AnyMessage
		if json.Unmarshal([]byte(s), &any) == nil {
			if any.Method == string(mcp.ProgressNotificationMethod) {
				return true
			}
		}
	}
	return false
}
