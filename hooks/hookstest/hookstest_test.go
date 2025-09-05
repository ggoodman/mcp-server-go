package hookstest_test

import (
	"context"
	"testing"

	"github.com/ggoodman/mcp-streaming-http-go/hooks"
	"github.com/ggoodman/mcp-streaming-http-go/hooks/hookstest"
	"github.com/ggoodman/mcp-streaming-http-go/mcp"
)

func TestMockCapabilities(t *testing.T) {
	ctx := context.Background()

	t.Run("MockToolsCapability", func(t *testing.T) {
		// Create a tool for testing
		testTool := mcp.Tool{
			Name:        "test-tool",
			Description: stringPtr("A test tool"),
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]mcp.SchemaProperty{
					"input": {
						Type:        "string",
						Description: stringPtr("Test input"),
					},
				},
				Required: []string{"input"},
			},
		}

		// Test with custom tools and handler
		toolsCapability := hookstest.NewMockToolsCapability(
			hookstest.WithTools(testTool),
			hookstest.WithCallToolHandler(func(ctx context.Context, session hooks.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{
					Content: []mcp.ContentBlock{
						{
							Type: "text",
							Text: stringPtr("Tool executed successfully"),
						},
					},
				}, nil
			}),
			hookstest.WithToolsChangedSupport(),
		)

		// Test ListTools
		tools, nextCursor, err := toolsCapability.ListTools(ctx, &mockSession{}, nil)
		if err != nil {
			t.Fatalf("ListTools failed: %v", err)
		}
		if len(tools) != 1 || tools[0].Name != "test-tool" {
			t.Errorf("Expected 1 tool named 'test-tool', got %d tools", len(tools))
		}
		if nextCursor != nil {
			t.Errorf("Expected nextCursor to be nil for mock implementation, got %s", *nextCursor)
		}

		// Test CallTool
		result, err := toolsCapability.CallTool(ctx, &mockSession{}, &mcp.CallToolRequestReceived{
			Name: "test-tool",
		})
		if err != nil {
			t.Fatalf("CallTool failed: %v", err)
		}
		if len(result.Content) == 0 || result.Content[0].Text == nil || *result.Content[0].Text != "Tool executed successfully" {
			t.Errorf("Unexpected tool result")
		}

		// Test tools changed listener registration
		registered, err := toolsCapability.RegisterToolsChangedListener(ctx, &mockSession{}, func(ctx context.Context) error {
			return nil
		})
		if err != nil {
			t.Fatalf("RegisterToolsChangedListener failed: %v", err)
		}
		if !registered {
			t.Errorf("Expected tools changed listener to be registered")
		}
	})

	t.Run("MockResourcesCapability", func(t *testing.T) {
		testResource := mcp.Resource{
			URI:         "file:///test.txt",
			Name:        "test.txt",
			Description: stringPtr("A test resource"),
			MimeType:    stringPtr("text/plain"),
		}

		testContents := []mcp.ResourceContents{
			{
				URI:      "file:///test.txt",
				MimeType: stringPtr("text/plain"),
				Text:     stringPtr("Hello, world!"),
			},
		}

		resourcesCapability := hookstest.NewMockResourcesCapability(
			hookstest.WithResources(testResource),
			hookstest.WithResourceContents("file:///test.txt", testContents),
			hookstest.WithResourcesListChangedSupport(),
			hookstest.WithResourceUpdatedSupport(),
		)

		// Test ListResources
		resources, _, err := resourcesCapability.ListResources(ctx, &mockSession{}, nil)
		if err != nil {
			t.Fatalf("ListResources failed: %v", err)
		}
		if len(resources) != 1 || resources[0].URI != "file:///test.txt" {
			t.Errorf("Expected 1 resource, got %d", len(resources))
		}

		// Test ReadResource
		contents, err := resourcesCapability.ReadResource(ctx, &mockSession{}, "file:///test.txt")
		if err != nil {
			t.Fatalf("ReadResource failed: %v", err)
		}
		if len(contents) != 1 || contents[0].Text == nil || *contents[0].Text != "Hello, world!" {
			t.Errorf("Unexpected resource contents")
		}
	})

	t.Run("MockPromptsCapability", func(t *testing.T) {
		testPrompt := mcp.Prompt{
			Name:        "test-prompt",
			Description: stringPtr("A test prompt"),
			Arguments: []mcp.PromptArgument{
				{
					Name:        "topic",
					Description: stringPtr("The topic to discuss"),
					Required:    boolPtr(true),
				},
			},
		}

		testPromptResult := &mcp.GetPromptResult{
			Description: stringPtr("Test prompt result"),
			Messages: []mcp.PromptMessage{
				{
					Role: mcp.RoleUser,
					Content: []mcp.ContentBlock{
						{
							Type: "text",
							Text: stringPtr("Tell me about test topic"),
						},
					},
				},
			},
		}

		promptsCapability := hookstest.NewMockPromptsCapability(
			hookstest.WithPrompts(testPrompt),
			hookstest.WithPromptResult("test-prompt", testPromptResult),
			hookstest.WithPromptsListChangedSupport(),
		)

		// Test ListPrompts
		prompts, _, err := promptsCapability.ListPrompts(ctx, &mockSession{}, nil)
		if err != nil {
			t.Fatalf("ListPrompts failed: %v", err)
		}
		if len(prompts) != 1 || prompts[0].Name != "test-prompt" {
			t.Errorf("Expected 1 prompt named 'test-prompt', got %d prompts", len(prompts))
		}

		// Test GetPrompt
		result, err := promptsCapability.GetPrompt(ctx, &mockSession{}, &mcp.GetPromptRequestReceived{
			Name: "test-prompt",
		})
		if err != nil {
			t.Fatalf("GetPrompt failed: %v", err)
		}
		if len(result.Messages) != 1 {
			t.Errorf("Expected 1 message, got %d", len(result.Messages))
		}
	})

	t.Run("MockLoggingCapability", func(t *testing.T) {
		loggingCapability := hookstest.NewMockLoggingCapability(
			hookstest.WithInitialLogLevel(mcp.LoggingLevelDebug),
			hookstest.WithLoggingMessageSupport(),
		)

		// Test initial log level
		if loggingCapability.GetCurrentLevel() != mcp.LoggingLevelDebug {
			t.Errorf("Expected initial log level to be debug")
		}

		// Test SetLevel
		err := loggingCapability.SetLevel(ctx, &mockSession{}, mcp.LoggingLevelError)
		if err != nil {
			t.Fatalf("SetLevel failed: %v", err)
		}
		if loggingCapability.GetCurrentLevel() != mcp.LoggingLevelError {
			t.Errorf("Expected log level to be error after setting")
		}

		// Test logging message listener registration
		registered, err := loggingCapability.RegisterLoggingMessageListener(ctx, &mockSession{}, func(ctx context.Context, level mcp.LoggingLevel, data any, logger *string) error {
			return nil
		})
		if err != nil {
			t.Fatalf("RegisterLoggingMessageListener failed: %v", err)
		}
		if !registered {
			t.Errorf("Expected logging message listener to be registered")
		}
	})

	t.Run("MockCompletionsCapability", func(t *testing.T) {
		completionsCapability := hookstest.NewMockCompletionsCapability(
			hookstest.WithCompletionHandler(func(ctx context.Context, session hooks.Session, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
				return &mcp.CompleteResult{
					Completion: mcp.Completion{
						Values:  []string{"completion1", "completion2"},
						Total:   intPtr(2),
						HasMore: boolPtr(false),
					},
				}, nil
			}),
		)

		// Test Complete
		result, err := completionsCapability.Complete(ctx, &mockSession{}, &mcp.CompleteRequest{
			Ref: mcp.ResourceReference{
				Type: "resource",
				URI:  "file:///test.txt",
			},
			Argument: mcp.CompleteArgument{
				Name:  "test",
				Value: "val",
			},
		})
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}
		if len(result.Completion.Values) != 2 {
			t.Errorf("Expected 2 completions, got %d", len(result.Completion.Values))
		}
	})
}

// mockSession implements hooks.Session for testing
type mockSession struct{}

func (m *mockSession) SessionID() string { return "test-session-id" }
func (m *mockSession) UserID() string    { return "test-user-id" }

// Helper functions for pointer creation
func stringPtr(s string) *string { return &s }
func boolPtr(b bool) *bool       { return &b }
func intPtr(i int) *int          { return &i }
