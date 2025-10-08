package mcpservice

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// simple no-op session implementation for tests (use zero value) - if a real one exists in tests, adapt.

type nopSession struct{ sessions.Session }

type emptyArgs struct{}
type emptyArgs2 struct{}

func TestNewTool_WithMeta_IncludedInSnapshot(t *testing.T) {
	tool := NewTool[emptyArgs]("echo", func(ctx context.Context, s sessions.Session, w ToolResponseWriter, r *ToolRequest[emptyArgs]) error {
		w.AppendText("ok")
		return nil
	}, WithToolDescription("echo tool"), WithToolMeta(map[string]any{"category": "test", "version": 2}))

	c := NewToolsContainer(tool)
	list := c.Snapshot()
	if len(list) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(list))
	}
	if list[0].Meta == nil {
		b, _ := json.Marshal(list[0])
		t.Fatalf("expected meta, got nil. tool=%s", string(b))
	}
	if got := list[0].Meta["category"]; got != "test" {
		b, _ := json.Marshal(list[0].Meta)
		t.Fatalf("expected category 'test', got %v meta=%s", got, string(b))
	}
	if got := list[0].Meta["version"]; got != 2 {
		b, _ := json.Marshal(list[0].Meta)
		t.Fatalf("expected version 2, got %v meta=%s", got, string(b))
	}
}

func TestNewTool_WithOutput_MetaIncluded(t *testing.T) {
	// tool with output schema
	type out struct {
		Value string `json:"value"`
	}
	tool := NewToolWithOutput[emptyArgs2, out]("produce", func(ctx context.Context, s sessions.Session, w ToolResponseWriterTyped[out], r *ToolRequest[emptyArgs2]) error {
		w.SetStructured(out{Value: "hi"})
		return nil
	}, WithToolMeta(map[string]any{"kind": "producer"}))

	c := NewToolsContainer(tool)
	page, err := c.ListTools(context.Background(), nopSession{}, nil)
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(page.Items) != 1 {
		b, _ := json.Marshal(page.Items)
		t.Fatalf("expected 1 tool, got %d: %s", len(page.Items), string(b))
	}
	if page.Items[0].Meta == nil || page.Items[0].Meta["kind"] != "producer" {
		b, _ := json.Marshal(page.Items[0])
		t.Fatalf("expected meta kind=producer, got %s", string(b))
	}
	if page.Items[0].OutputSchema == nil || page.Items[0].OutputSchema.Type != "object" {
		b, _ := json.Marshal(page.Items[0].OutputSchema)
		t.Fatalf("expected output schema object, got %s", string(b))
	}
}

func TestNewTool_NoMeta_OmitsField(t *testing.T) {
	tool := NewTool[emptyArgs]("plain", func(ctx context.Context, s sessions.Session, w ToolResponseWriter, r *ToolRequest[emptyArgs]) error {
		return nil
	})
	c := NewToolsContainer(tool)
	list := c.Snapshot()
	if len(list) != 1 {
		panic("expected 1 tool")
	}
	if list[0].Meta != nil {
		b, _ := json.Marshal(list[0])
		// Should be omitted when empty
		var raw map[string]any
		_ = json.Unmarshal(b, &raw)
		if _, ok := raw["_meta"]; ok {
			// meta should not be present at all
			panic("_meta should be omitted for empty meta")
		}
	}
}

// Ensure tool call result meta unaffected by tool descriptor meta.
func TestToolDescriptorMeta_DoesNotLeakToCallResult(t *testing.T) {
	tool := NewTool[emptyArgs]("echo", func(ctx context.Context, s sessions.Session, w ToolResponseWriter, r *ToolRequest[emptyArgs]) error {
		w.AppendText("hello")
		return nil
	}, WithToolMeta(map[string]any{"tag": "descriptor"}))
	c := NewToolsContainer(tool)
	res, err := c.Call(context.Background(), nopSession{}, &mcp.CallToolRequestReceived{Name: "echo"})
	if err != nil {
		t.Fatalf("call error: %v", err)
	}
	if res.Meta != nil { // CallToolResult inherits from BaseMetadata
		b, _ := json.Marshal(res)
		t.Fatalf("expected no meta on result (unless handler sets), got %s", string(b))
	}
}
