package stdio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
)

func TestInitializeAndPing(t *testing.T) {
	t.Parallel()

	// Prepare server capabilities
	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "stdio-test", Version: "0.1.0"}),
	)

	// Input: initialize + ping, newline-delimited
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  string(mcp.InitializeMethod),
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.0.0"},
		},
	}
	pingReq := map[string]any{"jsonrpc": "2.0", "id": 2, "method": string(mcp.PingMethod)}

	var in bytes.Buffer
	mustWriteJSONL(t, &in, initReq)
	mustWriteJSONL(t, &in, pingReq)

	var out bytes.Buffer
	h := NewHandler(server, WithIO(&in, &out))

	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	lines := splitNonEmptyLines(out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d: %q", len(lines), out.String())
	}

	// Validate initialize response
	var initRes jsonrpc.Response
	if err := json.Unmarshal([]byte(lines[0]), &initRes); err != nil {
		t.Fatalf("unmarshal init response: %v", err)
	}
	if initRes.Error != nil {
		t.Fatalf("init error: %+v", initRes.Error)
	}
	var initPayload mcp.InitializeResult
	if err := json.Unmarshal(initRes.Result, &initPayload); err != nil {
		t.Fatalf("unmarshal init payload: %v", err)
	}
	if got, want := initPayload.ProtocolVersion, "2025-06-18"; got != want {
		t.Fatalf("protocol version: got %q want %q", got, want)
	}
	if initPayload.ServerInfo.Name != "stdio-test" || initPayload.ServerInfo.Version != "0.1.0" {
		t.Fatalf("server info mismatch: %+v", initPayload.ServerInfo)
	}

	// Validate ping response
	var pingRes jsonrpc.Response
	if err := json.Unmarshal([]byte(lines[1]), &pingRes); err != nil {
		t.Fatalf("unmarshal ping response: %v", err)
	}
	if pingRes.Error != nil {
		t.Fatalf("ping error: %+v", pingRes.Error)
	}
}

func TestToolsList(t *testing.T) {
	t.Parallel()

	// Tools capability with one tool
	tool := mcp.Tool{
		Name:        "echo",
		Description: "Echo input",
		InputSchema: mcp.ToolInputSchema{Type: "object"},
	}
	tools := mcpservice.NewToolsCapability(
		mcpservice.WithListTools(func(_ context.Context, _ sessions.Session, _ *string) (mcpservice.Page[mcp.Tool], error) {
			return mcpservice.NewPage([]mcp.Tool{tool}), nil
		}),
	)

	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "stdio-tools", Version: "0.1.0"}),
		mcpservice.WithToolsCapability(tools),
	)

	// initialize + tools/list
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  string(mcp.InitializeMethod),
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.0.0"},
		},
	}
	listReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  string(mcp.ToolsListMethod),
		"params":  map[string]any{"cursor": ""},
	}

	var in bytes.Buffer
	mustWriteJSONL(t, &in, initReq)
	mustWriteJSONL(t, &in, listReq)
	var out bytes.Buffer

	h := NewHandler(server, WithIO(&in, &out))
	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	lines := splitNonEmptyLines(out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d: %q", len(lines), out.String())
	}

	// tools/list response
	var toolsRes jsonrpc.Response
	if err := json.Unmarshal([]byte(lines[1]), &toolsRes); err != nil {
		t.Fatalf("unmarshal tools response: %v", err)
	}
	if toolsRes.Error != nil {
		t.Fatalf("tools list error: %+v", toolsRes.Error)
	}
	var payload mcp.ListToolsResult
	if err := json.Unmarshal(toolsRes.Result, &payload); err != nil {
		t.Fatalf("unmarshal tools payload: %v", err)
	}
	if len(payload.Tools) != 1 || payload.Tools[0].Name != "echo" {
		t.Fatalf("unexpected tools payload: %+v", payload)
	}
}

func TestResourcesListAndRead(t *testing.T) {
	t.Parallel()

	res := mcp.Resource{URI: "res://hello.txt", Name: "hello.txt"}
	contents := []mcp.ResourceContents{{URI: res.URI, Text: "hello"}}

	resources := mcpservice.NewResourcesCapability(
		mcpservice.WithListResources(func(_ context.Context, _ sessions.Session, _ *string) (mcpservice.Page[mcp.Resource], error) {
			return mcpservice.NewPage([]mcp.Resource{res}), nil
		}),
		mcpservice.WithReadResource(func(_ context.Context, _ sessions.Session, uri string) ([]mcp.ResourceContents, error) {
			if uri != res.URI {
				return nil, nil
			}
			return contents, nil
		}),
	)

	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "stdio-resources", Version: "0.1.0"}),
		mcpservice.WithResourcesCapability(resources),
	)

	// initialize + resources/list + resources/read
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  string(mcp.InitializeMethod),
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.0.0"},
		},
	}
	listReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  string(mcp.ResourcesListMethod),
		"params":  map[string]any{"cursor": ""},
	}
	readReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  string(mcp.ResourcesReadMethod),
		"params":  map[string]any{"uri": res.URI},
	}

	var in bytes.Buffer
	mustWriteJSONL(t, &in, initReq)
	mustWriteJSONL(t, &in, listReq)
	mustWriteJSONL(t, &in, readReq)
	var out bytes.Buffer

	h := NewHandler(server, WithIO(&in, &out))
	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	lines := splitNonEmptyLines(out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 3 responses, got %d: %q", len(lines), out.String())
	}

	// resources/list
	var listRes jsonrpc.Response
	if err := json.Unmarshal([]byte(lines[1]), &listRes); err != nil {
		t.Fatalf("unmarshal resources/list: %v", err)
	}
	if listRes.Error != nil {
		t.Fatalf("resources/list error: %+v", listRes.Error)
	}
	var listPayload mcp.ListResourcesResult
	if err := json.Unmarshal(listRes.Result, &listPayload); err != nil {
		t.Fatalf("unmarshal resources payload: %v", err)
	}
	if len(listPayload.Resources) != 1 || listPayload.Resources[0].URI != res.URI {
		t.Fatalf("unexpected resources payload: %+v", listPayload)
	}

	// resources/read
	var readRes jsonrpc.Response
	if err := json.Unmarshal([]byte(lines[2]), &readRes); err != nil {
		t.Fatalf("unmarshal resources/read: %v", err)
	}
	if readRes.Error != nil {
		t.Fatalf("resources/read error: %+v", readRes.Error)
	}
	var readPayload mcp.ReadResourceResult
	if err := json.Unmarshal(readRes.Result, &readPayload); err != nil {
		t.Fatalf("unmarshal read payload: %v", err)
	}
	if len(readPayload.Contents) != 1 || readPayload.Contents[0].Text != "hello" {
		t.Fatalf("unexpected read payload: %+v", readPayload)
	}
}

func TestResourcesTemplatesList(t *testing.T) {
	t.Parallel()

	tmpl := mcp.ResourceTemplate{URITemplate: "file://{path}", Name: "file"}
	resources := mcpservice.NewResourcesCapability(
		mcpservice.WithListResourceTemplates(func(_ context.Context, _ sessions.Session, _ *string) (mcpservice.Page[mcp.ResourceTemplate], error) {
			return mcpservice.NewPage([]mcp.ResourceTemplate{tmpl}), nil
		}),
	)

	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "stdio-res-templates", Version: "0.1.0"}),
		mcpservice.WithResourcesCapability(resources),
	)

	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  string(mcp.InitializeMethod),
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.0.0"},
		},
	}
	listReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  string(mcp.ResourcesTemplatesListMethod),
		"params":  map[string]any{"cursor": ""},
	}

	var in bytes.Buffer
	mustWriteJSONL(t, &in, initReq)
	mustWriteJSONL(t, &in, listReq)
	var out bytes.Buffer

	h := NewHandler(server, WithIO(&in, &out))
	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	lines := splitNonEmptyLines(out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d: %q", len(lines), out.String())
	}

	var rpcRes jsonrpc.Response
	if err := json.Unmarshal([]byte(lines[1]), &rpcRes); err != nil {
		t.Fatalf("unmarshal templates response: %v", err)
	}
	if rpcRes.Error != nil {
		t.Fatalf("templates list error: %+v", rpcRes.Error)
	}
	var payload mcp.ListResourceTemplatesResult
	if err := json.Unmarshal(rpcRes.Result, &payload); err != nil {
		t.Fatalf("unmarshal templates payload: %v", err)
	}
	if len(payload.ResourceTemplates) != 1 || payload.ResourceTemplates[0].URITemplate != tmpl.URITemplate {
		t.Fatalf("unexpected templates payload: %+v", payload)
	}
}

func TestPromptsListAndGet(t *testing.T) {
	t.Parallel()

	p := mcp.Prompt{Name: "hello", Description: "say hello"}
	prompts := mcpservice.NewPromptsCapability(
		mcpservice.WithListPrompts(func(_ context.Context, _ sessions.Session, _ *string) (mcpservice.Page[mcp.Prompt], error) {
			return mcpservice.NewPage([]mcp.Prompt{p}), nil
		}),
		mcpservice.WithGetPrompt(func(_ context.Context, _ sessions.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error) {
			if req.Name != p.Name {
				return nil, nil
			}
			return &mcp.GetPromptResult{
				Description: "say hello",
				Messages: []mcp.PromptMessage{{
					Role:    mcp.RoleAssistant,
					Content: []mcp.ContentBlock{{Type: "text", Text: "hello"}},
				}},
			}, nil
		}),
	)

	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "stdio-prompts", Version: "0.1.0"}),
		mcpservice.WithPromptsCapability(prompts),
	)

	// initialize + prompts/list + prompts/get
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  string(mcp.InitializeMethod),
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.0.0"},
		},
	}
	listReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  string(mcp.PromptsListMethod),
		"params":  map[string]any{"cursor": ""},
	}
	getReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  string(mcp.PromptsGetMethod),
		"params":  map[string]any{"name": p.Name},
	}

	var in bytes.Buffer
	mustWriteJSONL(t, &in, initReq)
	mustWriteJSONL(t, &in, listReq)
	mustWriteJSONL(t, &in, getReq)
	var out bytes.Buffer

	h := NewHandler(server, WithIO(&in, &out))
	if err := h.Serve(context.Background()); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	lines := splitNonEmptyLines(out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 3 responses, got %d: %q", len(lines), out.String())
	}

	// prompts/list
	var listRes jsonrpc.Response
	if err := json.Unmarshal([]byte(lines[1]), &listRes); err != nil {
		t.Fatalf("unmarshal prompts/list: %v", err)
	}
	if listRes.Error != nil {
		t.Fatalf("prompts/list error: %+v", listRes.Error)
	}
	var listPayload mcp.ListPromptsResult
	if err := json.Unmarshal(listRes.Result, &listPayload); err != nil {
		t.Fatalf("unmarshal prompts payload: %v", err)
	}
	if len(listPayload.Prompts) != 1 || listPayload.Prompts[0].Name != p.Name {
		t.Fatalf("unexpected prompts payload: %+v", listPayload)
	}

	// prompts/get
	var getRes jsonrpc.Response
	if err := json.Unmarshal([]byte(lines[2]), &getRes); err != nil {
		t.Fatalf("unmarshal prompts/get: %v", err)
	}
	if getRes.Error != nil {
		t.Fatalf("prompts/get error: %+v", getRes.Error)
	}
	var getPayload mcp.GetPromptResult
	if err := json.Unmarshal(getRes.Result, &getPayload); err != nil {
		t.Fatalf("unmarshal get payload: %v", err)
	}
	if len(getPayload.Messages) != 1 || getPayload.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("unexpected get payload: %+v", getPayload)
	}
}

func TestResourcesSubscribeAndListChanged(t *testing.T) {
	t.Parallel()

	// Static resources container enables subscribe + listChanged
	sr := mcpservice.NewStaticResources(
		[]mcp.Resource{{URI: "res://a.txt", Name: "a.txt"}},
		nil,
		map[string][]mcp.ResourceContents{
			"res://a.txt": {{URI: "res://a.txt", Text: "A"}},
		},
	)

	resources := mcpservice.NewResourcesCapability(
		mcpservice.WithStaticResourceContainer(sr),
	)

	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "stdio-res-subs", Version: "0.1.0"}),
		mcpservice.WithResourcesCapability(resources),
	)

	// Build input: initialize -> subscribe -> (server change) -> unsubscribe
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  string(mcp.InitializeMethod),
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.0.0"},
		},
	}
	subReq := map[string]any{"jsonrpc": "2.0", "id": 2, "method": string(mcp.ResourcesSubscribeMethod), "params": map[string]any{"uri": "res://a.txt"}}
	unsubReq := map[string]any{"jsonrpc": "2.0", "id": 3, "method": string(mcp.ResourcesUnsubscribeMethod), "params": map[string]any{"uri": "res://a.txt"}}

	// Use pipes to simulate streaming stdio safely across goroutines
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	h := NewHandler(server, WithIO(inR, outW))

	// Start a reader to prevent PipeWriter flush from blocking
	var (
		lines    []string
		linesMu  sync.Mutex
		scanDone = make(chan struct{})
	)
	go func() {
		scanner := bufio.NewScanner(outR)
		for scanner.Scan() {
			s := strings.TrimSpace(scanner.Text())
			if s != "" {
				linesMu.Lock()
				lines = append(lines, s)
				linesMu.Unlock()
			}
		}
		close(scanDone)
	}()

	// Run Serve in background
	done := make(chan error, 1)
	go func() { done <- h.Serve(context.Background()) }()

	// Write initialize and subscribe
	mustWriteJSONLToWriter(t, inW, initReq)
	// Signal that the client finished initialization
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "method": string(mcp.InitializedNotificationMethod)})
	// Synchronize: send a ping and wait for init+ping responses so registrations are active
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "id": 2, "method": string(mcp.PingMethod)})
	waitForLines(t, &linesMu, &lines, 2)
	mustWriteJSONLToWriter(t, inW, subReq)

	// Trigger a list change by adding a new resource; should yield a list_changed notification.
	_ = sr.AddResource(context.Background(), mcp.Resource{URI: "res://b.txt", Name: "b.txt"})
	// Wait until we observe the notification before proceeding to avoid race with shutdown.
	waitForNotification(t, &linesMu, &lines, string(mcp.ResourcesListChangedNotificationMethod))

	// Now request unsubscribe and close input to end the handler loop gracefully
	mustWriteJSONLToWriter(t, inW, unsubReq)
	_ = inW.Close() // signal EOF

	// Wait for Serve to return, then close output writer and wait for reader
	<-done
	_ = outW.Close()
	<-scanDone

	linesMu.Lock()
	defer linesMu.Unlock()
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 messages (init, subscribe result, list_changed), got %d: %+v", len(lines), lines)
	}
	// Find a resources list_changed notification among messages
	foundListChanged := false
	for _, l := range lines {
		var any jsonrpc.AnyMessage
		if err := json.Unmarshal([]byte(l), &any); err != nil {
			continue
		}
		if any.Type() == "notification" && any.Method == string(mcp.ResourcesListChangedNotificationMethod) {
			foundListChanged = true
			break
		}
	}
	if !foundListChanged {
		t.Fatalf("did not observe resources list_changed notification; outputs: %+v", lines)
	}
}

func TestToolsListChangedNotification(t *testing.T) {
	t.Parallel()

	// Static tools container enables listChanged
	st := mcpservice.NewStaticTools()
	toolsCap := mcpservice.NewToolsCapability(
		mcpservice.WithStaticToolsContainer(st),
	)

	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "stdio-tools-lc", Version: "0.1.0"}),
		mcpservice.WithToolsCapability(toolsCap),
	)

	// Use pipes to simulate streaming stdio
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	// concurrent reader to drain outputs
	var (
		lines    []string
		linesMu  sync.Mutex
		scanDone = make(chan struct{})
	)
	go func() {
		scanner := bufio.NewScanner(outR)
		for scanner.Scan() {
			s := strings.TrimSpace(scanner.Text())
			if s != "" {
				linesMu.Lock()
				lines = append(lines, s)
				linesMu.Unlock()
			}
		}
		close(scanDone)
	}()

	h := NewHandler(server, WithIO(inR, outW))
	done := make(chan error, 1)
	go func() { done <- h.Serve(context.Background()) }()

	// Write initialize
	mustWriteJSONLToWriter(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  string(mcp.InitializeMethod),
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.0.0"},
		},
	})
	// Send notifications/initialized to mark client readiness for notifications
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "method": string(mcp.InitializedNotificationMethod)})

	// Synchronize: send a ping and wait for init+ping responses so registrations are active
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "id": 2, "method": string(mcp.PingMethod)})
	waitForLines(t, &linesMu, &lines, 2)

	// Trigger tools change -> should emit list_changed
	st.Add(context.Background(), mcpservice.StaticTool{
		Descriptor: mcp.Tool{Name: "noop", InputSchema: mcp.ToolInputSchema{Type: "object"}},
		Handler: func(ctx context.Context, _ sessions.Session, _ *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		},
	})

	// Wait for the tools list_changed notification to appear
	waitForNotification(t, &linesMu, &lines, string(mcp.ToolsListChangedNotificationMethod))

	_ = inW.Close() // signal EOF
	<-done
	_ = outW.Close()
	<-scanDone

	linesMu.Lock()
	defer linesMu.Unlock()
	found := false
	for _, l := range lines {
		var any jsonrpc.AnyMessage
		if err := json.Unmarshal([]byte(l), &any); err != nil {
			continue
		}
		if any.Type() == "notification" && any.Method == string(mcp.ToolsListChangedNotificationMethod) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("did not observe tools list_changed notification; outputs: %+v", lines)
	}
}

func TestPromptsListChangedNotification(t *testing.T) {
	t.Parallel()

	sp := mcpservice.NewStaticPrompts()
	promptsCap := mcpservice.NewPromptsCapability(
		mcpservice.WithStaticPromptsContainer(sp),
	)

	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "stdio-prompts-lc", Version: "0.1.0"}),
		mcpservice.WithPromptsCapability(promptsCap),
	)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	var (
		lines    []string
		linesMu  sync.Mutex
		scanDone = make(chan struct{})
	)
	go func() {
		scanner := bufio.NewScanner(outR)
		for scanner.Scan() {
			s := strings.TrimSpace(scanner.Text())
			if s != "" {
				linesMu.Lock()
				lines = append(lines, s)
				linesMu.Unlock()
			}
		}
		close(scanDone)
	}()

	h := NewHandler(server, WithIO(inR, outW))
	done := make(chan error, 1)
	go func() { done <- h.Serve(context.Background()) }()

	mustWriteJSONLToWriter(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  string(mcp.InitializeMethod),
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.0.0"},
		},
	})

	// Send notifications/initialized to mark client readiness for notifications
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "method": string(mcp.InitializedNotificationMethod)})

	// Synchronize: send a ping and wait for init+ping responses so registrations are active
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "id": 2, "method": string(mcp.PingMethod)})
	waitForLines(t, &linesMu, &lines, 2)

	// Trigger prompts change -> should emit list_changed
	sp.Add(context.Background(), mcpservice.StaticPrompt{
		Descriptor: mcp.Prompt{Name: "hi"},
		Handler: func(ctx context.Context, _ sessions.Session, _ *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Messages: []mcp.PromptMessage{}}, nil
		},
	})

	// Wait for the prompts list_changed notification to appear
	waitForNotification(t, &linesMu, &lines, string(mcp.PromptsListChangedNotificationMethod))

	_ = inW.Close()
	<-done
	_ = outW.Close()
	<-scanDone

	linesMu.Lock()
	defer linesMu.Unlock()
	found := false
	for _, l := range lines {
		var any jsonrpc.AnyMessage
		if err := json.Unmarshal([]byte(l), &any); err != nil {
			continue
		}
		if any.Type() == "notification" && any.Method == string(mcp.PromptsListChangedNotificationMethod) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("did not observe prompts list_changed notification; outputs: %+v", lines)
	}
}

// helpers
func mustWriteJSONL(t *testing.T, buf *bytes.Buffer, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := buf.Write(append(b, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func splitNonEmptyLines(s string) []string {
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Write helper for io.Writer targets (e.g., pipes)
func mustWriteJSONLToWriter(t *testing.T, w io.Writer, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// waitForLines waits until the collected output lines reach at least n, or times out.
func waitForLines(t *testing.T, mu *sync.Mutex, lines *[]string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		l := len(*lines)
		mu.Unlock()
		if l >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	l := len(*lines)
	mu.Unlock()
	t.Fatalf("timeout waiting for %d lines, saw %d", n, l)
}

// waitForNotification waits until a notification with the given method appears in the collected lines.
func waitForNotification(t *testing.T, mu *sync.Mutex, lines *[]string, method string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		snapshot := append([]string(nil), (*lines)...)
		mu.Unlock()
		for _, l := range snapshot {
			var any jsonrpc.AnyMessage
			if err := json.Unmarshal([]byte(l), &any); err != nil {
				continue
			}
			if any.Type() == "notification" && any.Method == method {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for notification %q", method)
}

func TestIgnoreClientResponseAndContinue(t *testing.T) {
	t.Parallel()

	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "stdio-ignore-response", Version: "0.1.0"}),
	)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	var (
		lines    []string
		linesMu  sync.Mutex
		scanDone = make(chan struct{})
	)
	go func() {
		scanner := bufio.NewScanner(outR)
		for scanner.Scan() {
			s := strings.TrimSpace(scanner.Text())
			if s != "" {
				linesMu.Lock()
				lines = append(lines, s)
				linesMu.Unlock()
			}
		}
		close(scanDone)
	}()

	h := NewHandler(server, WithIO(inR, outW))
	done := make(chan error, 1)
	go func() { done <- h.Serve(context.Background()) }()

	// Initialize
	mustWriteJSONLToWriter(t, inW, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": string(mcp.InitializeMethod),
		"params": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"}},
	})
	// Client sends notifications/initialized
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "method": string(mcp.InitializedNotificationMethod)})

	// Send a stray client response (should be ignored)
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "id": 999, "result": map[string]any{"ok": true}})

	// Then a valid ping request which should get a response
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "id": 2, "method": string(mcp.PingMethod)})

	// Close input and collect
	_ = inW.Close()
	<-done
	_ = outW.Close()
	<-scanDone

	linesMu.Lock()
	defer linesMu.Unlock()
	// Expect at least 2 responses: initialize result and ping result
	count := 0
	for _, l := range lines {
		var any jsonrpc.AnyMessage
		if err := json.Unmarshal([]byte(l), &any); err != nil {
			continue
		}
		if any.Type() == "response" {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("expected at least 2 server responses, got %d; lines=%v", count, lines)
	}
}

func TestAcceptProgressAndCancelledNotifications(t *testing.T) {
	t.Parallel()

	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "stdio-notifs", Version: "0.1.0"}),
	)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	var (
		lines    []string
		linesMu  sync.Mutex
		scanDone = make(chan struct{})
	)
	go func() {
		scanner := bufio.NewScanner(outR)
		for scanner.Scan() {
			s := strings.TrimSpace(scanner.Text())
			if s != "" {
				linesMu.Lock()
				lines = append(lines, s)
				linesMu.Unlock()
			}
		}
		close(scanDone)
	}()

	h := NewHandler(server, WithIO(inR, outW))
	done := make(chan error, 1)
	go func() { done <- h.Serve(context.Background()) }()

	// Initialize
	mustWriteJSONLToWriter(t, inW, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": string(mcp.InitializeMethod),
		"params": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"}},
	})
	// Client sends notifications/initialized
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "method": string(mcp.InitializedNotificationMethod)})

	// Send progress and cancelled notifications which should be accepted and ignored
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "method": string(mcp.ProgressNotificationMethod), "params": map[string]any{"progressToken": "t1", "progress": 0.5}})
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "method": string(mcp.CancelledNotificationMethod), "params": map[string]any{"requestId": "42"}})

	// Then send a ping request and ensure we still get a response
	mustWriteJSONLToWriter(t, inW, map[string]any{"jsonrpc": "2.0", "id": 2, "method": string(mcp.PingMethod)})

	_ = inW.Close()
	<-done
	_ = outW.Close()
	<-scanDone

	linesMu.Lock()
	defer linesMu.Unlock()
	// Expect at least 2 responses (init + ping). Notifications should not produce responses.
	respCount := 0
	for _, l := range lines {
		var any jsonrpc.AnyMessage
		if err := json.Unmarshal([]byte(l), &any); err != nil {
			continue
		}
		if any.Type() == "response" {
			respCount++
		}
	}
	if respCount < 2 {
		t.Fatalf("expected at least 2 responses, got %d; lines=%v", respCount, lines)
	}
}
