package stdio

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type testToolsCapability struct{}

func (testToolsCapability) ListTools(ctx context.Context, session sessions.Session, cursor *string) (mcpservice.Page[mcp.Tool], error) {
	return mcpservice.NewPage([]mcp.Tool{{
		Name:        "echo",
		Description: "Echo tool",
		InputSchema: mcp.ToolInputSchema{Type: "object"},
	}}), nil
}

func (testToolsCapability) CallTool(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
	mcpservice.ReportProgress(ctx, 0.5, 1.0)
	return &mcp.CallToolResult{
		Content: []mcp.ContentBlock{{Type: mcp.ContentTypeText, Text: "pong"}},
	}, nil
}

func (testToolsCapability) GetListChangedCapability(ctx context.Context, session sessions.Session) (mcpservice.ToolListChangedCapability, bool, error) {
	return nil, false, nil
}

func buildRequest(t *testing.T, id any, method string, params any) []byte {
	t.Helper()
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		raw = b
	}
	req := jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: method, Params: raw}
	if id != nil {
		req.ID = jsonrpc.NewRequestID(id)
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return append(b, '\n')
}

func buildNotification(t *testing.T, method string, params any) []byte {
	t.Helper()
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		raw = b
	}
	req := jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: method, Params: raw}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	return append(b, '\n')
}

func TestHandlerInitializeAndTools(t *testing.T) {
	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "test-server", Version: "1.0.0"}),
		mcpservice.WithToolsCapability(testToolsCapability{}),
	)

	initParams := mcp.InitializeRequest{
		ProtocolVersion: mcp.LatestProtocolVersion,
		ClientInfo:      mcp.ImplementationInfo{Name: "client", Version: "0.1.0"},
	}
	listParams := mcp.ListToolsRequest{}
	callParams := mcp.CallToolRequestReceived{Name: "echo", Arguments: json.RawMessage(`{"input":"ping"}`)}

	var input bytes.Buffer
	input.Write(buildRequest(t, 1, string(mcp.InitializeMethod), initParams))
	input.Write(buildRequest(t, 2, string(mcp.ToolsListMethod), listParams))
	input.Write(buildRequest(t, 3, string(mcp.ToolsCallMethod), callParams))

	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))

	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	responses := make(map[string]*jsonrpc.Response)
	progressSeen := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg jsonrpc.AnyMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("unmarshal message: %v", err)
		}
		switch msg.Type() {
		case "response":
			res := msg.AsResponse()
			if res == nil || res.ID == nil {
				t.Fatalf("response missing id: %s", line)
			}
			responses[res.ID.String()] = res
		case "notification":
			if msg.Method != string(mcp.ProgressNotificationMethod) {
				t.Fatalf("unexpected notification method %s", msg.Method)
			}
			var params mcp.ProgressNotificationParams
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("unmarshal progress params: %v", err)
			}
			if params.Progress != 0.5 {
				t.Fatalf("unexpected progress value: %v", params.Progress)
			}
			progressSeen = true
		default:
			t.Fatalf("unexpected message type %q", msg.Type())
		}
	}

	if len(responses) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(responses))
	}
	if !progressSeen {
		t.Fatalf("expected progress notification")
	}

	initRes := responses["1"]
	if initRes == nil {
		t.Fatalf("missing initialize response")
	}
	var initPayload mcp.InitializeResult
	if err := json.Unmarshal(initRes.Result, &initPayload); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if initPayload.ServerInfo.Name != "test-server" {
		t.Fatalf("unexpected server name: %s", initPayload.ServerInfo.Name)
	}
	if initPayload.Capabilities.Tools == nil {
		t.Fatalf("expected tools capability to be advertised")
	}

	listRes := responses["2"]
	if listRes == nil {
		t.Fatalf("missing tools/list response")
	}
	var listPayload mcp.ListToolsResult
	if err := json.Unmarshal(listRes.Result, &listPayload); err != nil {
		t.Fatalf("unmarshal list tools result: %v", err)
	}
	if len(listPayload.Tools) != 1 || listPayload.Tools[0].Name != "echo" {
		t.Fatalf("unexpected list tools payload: %+v", listPayload.Tools)
	}

	callRes := responses["3"]
	if callRes == nil {
		t.Fatalf("missing tools/call response")
	}
	var callPayload mcp.CallToolResult
	if err := json.Unmarshal(callRes.Result, &callPayload); err != nil {
		t.Fatalf("unmarshal call tool result: %v", err)
	}
	if len(callPayload.Content) == 0 || callPayload.Content[0].Text != "pong" {
		t.Fatalf("unexpected call tool content: %+v", callPayload.Content)
	}
}

func TestHandlerRejectsBatchRequests(t *testing.T) {
	server := mcpservice.NewServer()
	var input bytes.Buffer
	// A line starting with '[' must be rejected as batch request
	input.WriteString("[{}]\n")
	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))
	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	line := strings.TrimSpace(output.String())
	if line == "" {
		t.Fatalf("expected error response for batch request")
	}
	var msg jsonrpc.AnyMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if msg.Type() != "response" {
		t.Fatalf("expected response, got %s", msg.Type())
	}
	res := msg.AsResponse()
	if res.Error == nil || res.Error.Code != jsonrpc.ErrorCodeInvalidRequest {
		t.Fatalf("expected invalid request error, got %+v", res.Error)
	}
}

func TestHandlerSecondInitializeRejected(t *testing.T) {
	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "test-server", Version: "1.0.0"}),
	)
	initParams := mcp.InitializeRequest{ProtocolVersion: mcp.LatestProtocolVersion, ClientInfo: mcp.ImplementationInfo{Name: "client", Version: "0.1.0"}}
	var input bytes.Buffer
	input.Write(buildRequest(t, 1, string(mcp.InitializeMethod), initParams))
	input.Write(buildRequest(t, 2, string(mcp.InitializeMethod), initParams))
	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))
	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(lines))
	}
	var msg1, msg2 jsonrpc.AnyMessage
	_ = json.Unmarshal([]byte(lines[0]), &msg1)
	_ = json.Unmarshal([]byte(lines[1]), &msg2)
	res1 := msg1.AsResponse()
	res2 := msg2.AsResponse()
	if res1 == nil || res1.Error != nil || res1.Result == nil {
		t.Fatalf("first initialize should succeed, got %+v", res1)
	}
	if res2 == nil || res2.Error == nil || res2.Error.Code != jsonrpc.ErrorCodeInvalidRequest {
		t.Fatalf("second initialize should be invalid request, got %+v", res2)
	}
}

func TestHandlerRequestBeforeInitializeRejected(t *testing.T) {
	server := mcpservice.NewServer()
	var input bytes.Buffer
	input.Write(buildRequest(t, 1, string(mcp.ToolsListMethod), mcp.ListToolsRequest{}))
	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))
	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	var msg jsonrpc.AnyMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(output.String())), &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	res := msg.AsResponse()
	if res == nil || res.Error == nil || res.Error.Code != jsonrpc.ErrorCodeInvalidRequest {
		t.Fatalf("expected invalid request error before initialize, got %+v", res)
	}
}

func TestHandlerToolsListPaginationStatic(t *testing.T) {
	// Build static tools with 2 entries and page size 1
	type args struct {
		N string `json:"n,omitempty"`
	}
	st := mcpservice.NewStaticTools(
		mcpservice.NewTool[args]("a", func(ctx context.Context, _ sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[args]) error {
			_ = w.AppendText("A")
			return nil
		}),
		mcpservice.NewTool[args]("b", func(ctx context.Context, _ sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[args]) error {
			_ = w.AppendText("B")
			return nil
		}),
	)
	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "test-server", Version: "1.0.0"}),
		mcpservice.WithToolsOptions(
			mcpservice.WithStaticToolsContainer(st),
			mcpservice.WithToolsPageSize(1),
		),
	)

	init := mcp.InitializeRequest{ProtocolVersion: mcp.LatestProtocolVersion, ClientInfo: mcp.ImplementationInfo{Name: "client", Version: "0.1.0"}}
	list := mcp.ListToolsRequest{}
	var input bytes.Buffer
	input.Write(buildRequest(t, 1, string(mcp.InitializeMethod), init))
	input.Write(buildRequest(t, 2, string(mcp.ToolsListMethod), list))

	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))
	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(lines))
	}
	// Parse list result
	var msg2 jsonrpc.AnyMessage
	if err := json.Unmarshal([]byte(lines[1]), &msg2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	res2 := msg2.AsResponse()
	if res2 == nil || res2.Error != nil {
		t.Fatalf("tools/list failed: %+v", res2)
	}
	var payload mcp.ListToolsResult
	if err := json.Unmarshal(res2.Result, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(payload.Tools) != 1 || payload.NextCursor == "" {
		t.Fatalf("expected 1 tool and nextCursor, got %+v", payload)
	}

	// Request next page using nextCursor (fresh handler/session for simplicity)
	nextReq := mcp.ListToolsRequest{}
	nextReq.Cursor = payload.NextCursor
	var input2 bytes.Buffer
	input2.Write(buildRequest(t, 10, string(mcp.InitializeMethod), init))
	input2.Write(buildRequest(t, 11, string(mcp.ToolsListMethod), nextReq))
	output = syncBuffer{}
	h = NewHandler(server, WithReader(&input2), WithWriter(&output))
	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	lines = strings.Split(strings.TrimSpace(output.String()), "\n")
	var msgNext jsonrpc.AnyMessage
	if err := json.Unmarshal([]byte(lines[1]), &msgNext); err != nil {
		t.Fatalf("unmarshal next: %v", err)
	}
	resNext := msgNext.AsResponse()
	var payloadNext mcp.ListToolsResult
	if err := json.Unmarshal(resNext.Result, &payloadNext); err != nil {
		t.Fatalf("unmarshal next payload: %v", err)
	}
	if len(payloadNext.Tools) != 1 || payloadNext.NextCursor != "" {
		t.Fatalf("expected final page with 1 tool and no nextCursor, got %+v", payloadNext)
	}
}

func TestHandlerToolsCallInvalidParams(t *testing.T) {
	// Use default tools capability (no static container, no custom call) so it enforces missing name
	server := mcpservice.NewServer(mcpservice.WithToolsOptions())
	init := mcp.InitializeRequest{ProtocolVersion: mcp.LatestProtocolVersion, ClientInfo: mcp.ImplementationInfo{Name: "client", Version: "0.1.0"}}
	// Missing name field -> invalid params per protocol
	var badParams = map[string]any{"arguments": map[string]any{"x": 1}}
	var input bytes.Buffer
	input.Write(buildRequest(t, 1, string(mcp.InitializeMethod), init))
	input.Write(buildRequest(t, 2, string(mcp.ToolsCallMethod), badParams))
	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))
	_ = h.Serve(context.Background())
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(lines))
	}
	var msg jsonrpc.AnyMessage
	_ = json.Unmarshal([]byte(lines[1]), &msg)
	res := msg.AsResponse()
	if res == nil || res.Error == nil || res.Error.Code != jsonrpc.ErrorCodeInvalidParams {
		t.Fatalf("expected invalid params error, got %+v", res)
	}
}

func TestHandlerToolsCallErrorResult(t *testing.T) {
	// Static tool that returns an error result (isError=true)
	type failArgs struct {
		Fail bool `json:"fail"`
	}
	st := mcpservice.NewStaticTools(
		mcpservice.NewTool[failArgs]("maybe-fail", func(ctx context.Context, _ sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[failArgs]) error {
			if r.Args().Fail {
				w.SetError(true)
				_ = w.AppendText("boom")
			} else {
				_ = w.AppendText("ok")
			}
			return nil
		}),
	)
	server := mcpservice.NewServer(
		mcpservice.WithToolsOptions(mcpservice.WithStaticToolsContainer(st)),
	)
	init := mcp.InitializeRequest{ProtocolVersion: mcp.LatestProtocolVersion, ClientInfo: mcp.ImplementationInfo{Name: "client", Version: "0.1.0"}}
	call := mcp.CallToolRequestReceived{Name: "maybe-fail", Arguments: json.RawMessage(`{"fail":true}`)}
	var input bytes.Buffer
	input.Write(buildRequest(t, 1, string(mcp.InitializeMethod), init))
	input.Write(buildRequest(t, 2, string(mcp.ToolsCallMethod), call))
	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))
	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	var msg jsonrpc.AnyMessage
	_ = json.Unmarshal([]byte(lines[1]), &msg)
	res := msg.AsResponse()
	if res == nil || res.Error != nil {
		t.Fatalf("unexpected error response: %+v", res)
	}
	var payload mcp.CallToolResult
	if err := json.Unmarshal(res.Result, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !payload.IsError || len(payload.Content) == 0 || payload.Content[0].Text == "" {
		t.Fatalf("expected error result with text, got %+v", payload)
	}
}

func TestHandlerProgressMultipleUpdates(t *testing.T) {
	// Tool that reports multiple progress updates
	server := mcpservice.NewServer(
		mcpservice.WithToolsCapability(toolsCapProgressMulti{}),
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "test-server", Version: "1.0.0"}),
	)

	init := mcp.InitializeRequest{ProtocolVersion: mcp.LatestProtocolVersion, ClientInfo: mcp.ImplementationInfo{Name: "client", Version: "0.1.0"}}
	call := mcp.CallToolRequestReceived{Name: "echo", Arguments: json.RawMessage(`{}`)}
	var input bytes.Buffer
	input.Write(buildRequest(t, 1, string(mcp.InitializeMethod), init))
	input.Write(buildRequest(t, 2, string(mcp.ToolsCallMethod), call))
	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))
	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	// Expect: 1 response to initialize, 2 progress notifications, 1 final response => 4 messages
	if len(lines) != 4 {
		t.Fatalf("expected 4 messages, got %d: %v", len(lines), lines)
	}
	var progresses []mcp.ProgressNotificationParams
	for _, line := range lines {
		var any jsonrpc.AnyMessage
		_ = json.Unmarshal([]byte(line), &any)
		if any.Type() == "notification" && any.Method == string(mcp.ProgressNotificationMethod) {
			var p mcp.ProgressNotificationParams
			if err := json.Unmarshal(any.Params, &p); err == nil {
				progresses = append(progresses, p)
			}
		}
	}
	if len(progresses) != 2 {
		t.Fatalf("expected 2 progress updates, got %d", len(progresses))
	}
	// Correlate by request id "2"
	if tok, ok := progresses[0].ProgressToken.(string); !ok || tok != "2" {
		t.Fatalf("expected progressToken '2', got %#v", progresses[0].ProgressToken)
	}
	if tok, ok := progresses[1].ProgressToken.(string); !ok || tok != "2" {
		t.Fatalf("expected progressToken '2', got %#v", progresses[1].ProgressToken)
	}
	if progresses[0].Progress >= progresses[1].Progress {
		t.Fatalf("progress should be increasing: %+v", progresses)
	}
	if progresses[0].Total <= 0 || progresses[1].Total <= 0 {
		t.Fatalf("expected total to be set > 0")
	}
}

type toolsCapProgressMulti struct{}

func (toolsCapProgressMulti) ListTools(ctx context.Context, session sessions.Session, cursor *string) (mcpservice.Page[mcp.Tool], error) {
	return mcpservice.NewPage([]mcp.Tool{{Name: "echo", InputSchema: mcp.ToolInputSchema{Type: "object"}}}), nil
}
func (toolsCapProgressMulti) CallTool(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
	mcpservice.ReportProgress(ctx, 0.1, 1.0)
	mcpservice.ReportProgress(ctx, 0.9, 1.0)
	return &mcp.CallToolResult{Content: []mcp.ContentBlock{{Type: mcp.ContentTypeText, Text: "done"}}}, nil
}
func (toolsCapProgressMulti) GetListChangedCapability(ctx context.Context, session sessions.Session) (mcpservice.ToolListChangedCapability, bool, error) {
	return nil, false, nil
}

func TestHandlerCancellationStopsTool(t *testing.T) {
	// Tool blocks until ctx.Done()
	cap := blockingToolCapability{}
	server := mcpservice.NewServer(
		mcpservice.WithToolsCapability(cap),
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "srv", Version: "1"}),
	)
	init := mcp.InitializeRequest{ProtocolVersion: mcp.LatestProtocolVersion, ClientInfo: mcp.ImplementationInfo{Name: "client", Version: "0.1.0"}}
	call := mcp.CallToolRequestReceived{Name: "block", Arguments: json.RawMessage(`{}`)}
	var input bytes.Buffer
	input.Write(buildRequest(t, 1, string(mcp.InitializeMethod), init))
	input.Write(buildRequest(t, 2, string(mcp.ToolsCallMethod), call))
	// Send cancellation for request id "2" next
	input.Write(buildNotification(t, string(mcp.CancelledNotificationMethod), mcp.CancelledNotification{RequestID: "2", Reason: "test"}))
	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = h.Serve(ctx)

	// Look for a response to request id 2 that is an error OR a result with isError=true
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	var gotFinal bool
	for _, line := range lines {
		var any jsonrpc.AnyMessage
		_ = json.Unmarshal([]byte(line), &any)
		if any.Type() == "response" && any.ID != nil && any.ID.String() == "2" {
			res := any.AsResponse()
			if res.Error != nil {
				gotFinal = true
				break
			}
			var r mcp.CallToolResult
			if err := json.Unmarshal(res.Result, &r); err == nil && (r.IsError || (len(r.Content) > 0)) {
				gotFinal = true
				break
			}
		}
	}
	if !gotFinal {
		t.Fatalf("expected a final response to cancelled call")
	}
}

type blockingToolCapability struct{}

func (blockingToolCapability) ListTools(ctx context.Context, session sessions.Session, cursor *string) (mcpservice.Page[mcp.Tool], error) {
	return mcpservice.NewPage([]mcp.Tool{{Name: "block", InputSchema: mcp.ToolInputSchema{Type: "object"}}}), nil
}
func (blockingToolCapability) CallTool(ctx context.Context, session sessions.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
	select {
	case <-ctx.Done():
		return &mcp.CallToolResult{IsError: true, Content: []mcp.ContentBlock{{Type: mcp.ContentTypeText, Text: "cancelled"}}}, nil
	case <-time.After(2 * time.Second):
		return &mcp.CallToolResult{Content: []mcp.ContentBlock{{Type: mcp.ContentTypeText, Text: "done"}}}, nil
	}
}
func (blockingToolCapability) GetListChangedCapability(ctx context.Context, session sessions.Session) (mcpservice.ToolListChangedCapability, bool, error) {
	return nil, false, nil
}

func TestHandlerLoggingSetLevel(t *testing.T) {
	var lv slog.LevelVar
	server := mcpservice.NewServer(
		mcpservice.WithLoggingCapability(mcpservice.NewSlogLevelVarLogging(&lv)),
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "srv", Version: "1"}),
	)
	init := mcp.InitializeRequest{ProtocolVersion: mcp.LatestProtocolVersion, ClientInfo: mcp.ImplementationInfo{Name: "client", Version: "0.1.0"}}
	set := func(id int, level mcp.LoggingLevel) []byte {
		return buildRequest(t, id, string(mcp.LoggingSetLevelMethod), mcp.SetLevelRequest{Level: level})
	}

	var input bytes.Buffer
	input.Write(buildRequest(t, 1, string(mcp.InitializeMethod), init))
	input.Write(set(2, mcp.LoggingLevelDebug))
	input.Write(set(3, mcp.LoggingLevelInfo))
	input.Write(set(4, "not-a-level"))
	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))
	_ = h.Serve(context.Background())
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(lines))
	}
	// Check responses for 2,3 are success and 4 is invalid params (per spec)
	var msg2, msg3, msg4 jsonrpc.AnyMessage
	_ = json.Unmarshal([]byte(lines[1]), &msg2)
	_ = json.Unmarshal([]byte(lines[2]), &msg3)
	_ = json.Unmarshal([]byte(lines[3]), &msg4)
	if msg2.AsResponse().Error != nil || msg3.AsResponse().Error != nil {
		t.Fatalf("expected success for valid levels")
	}
	if err := msg4.AsResponse().Error; err == nil || err.Code != jsonrpc.ErrorCodeInvalidParams {
		t.Fatalf("expected invalid params for bad level, got %+v", err)
	}
}

func TestHandlerToolsCallNotFound(t *testing.T) {
	// Empty tools capability -> calling unknown tool should produce an error response
	server := mcpservice.NewServer(mcpservice.WithToolsOptions())
	init := mcp.InitializeRequest{ProtocolVersion: mcp.LatestProtocolVersion, ClientInfo: mcp.ImplementationInfo{Name: "client", Version: "0.1.0"}}
	call := mcp.CallToolRequestReceived{Name: "missing", Arguments: json.RawMessage(`{}`)}
	var input bytes.Buffer
	input.Write(buildRequest(t, 1, string(mcp.InitializeMethod), init))
	input.Write(buildRequest(t, 2, string(mcp.ToolsCallMethod), call))
	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))
	_ = h.Serve(context.Background())
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(lines))
	}
	var msg jsonrpc.AnyMessage
	_ = json.Unmarshal([]byte(lines[1]), &msg)
	res := msg.AsResponse()
	if res == nil || res.Error == nil {
		t.Fatalf("expected error response for missing tool, got %+v", res)
	}
}

func TestHandlerUnknownCancelIsNoop(t *testing.T) {
	server := mcpservice.NewServer(mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "srv", Version: "1"}))
	init := mcp.InitializeRequest{ProtocolVersion: mcp.LatestProtocolVersion, ClientInfo: mcp.ImplementationInfo{Name: "client", Version: "0.1.0"}}
	var input bytes.Buffer
	input.Write(buildRequest(t, 1, string(mcp.InitializeMethod), init))
	input.Write(buildNotification(t, string(mcp.CancelledNotificationMethod), mcp.CancelledNotification{RequestID: "999", Reason: "noop"}))
	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))
	_ = h.Serve(context.Background())
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	// Expect only the initialize response; cancellation is a notification and should not generate responses/errors
	if len(lines) != 1 {
		t.Fatalf("expected only 1 response (initialize), got %d: %v", len(lines), lines)
	}
}

func TestHandlerInvalidJSONRejected(t *testing.T) {
	server := mcpservice.NewServer()
	var input bytes.Buffer
	input.WriteString("{not json}\n")
	var output syncBuffer
	h := NewHandler(server, WithReader(&input), WithWriter(&output))
	_ = h.Serve(context.Background())
	line := strings.TrimSpace(output.String())
	if line == "" {
		t.Fatalf("expected an error response for invalid JSON")
	}
	var msg jsonrpc.AnyMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	res := msg.AsResponse()
	if res == nil || res.Error == nil || res.Error.Code != jsonrpc.ErrorCodeInvalidRequest {
		t.Fatalf("expected invalid request error, got %+v", res)
	}
}
