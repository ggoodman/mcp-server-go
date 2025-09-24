package stdio

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

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
