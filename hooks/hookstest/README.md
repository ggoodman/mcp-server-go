# Hooks Test Package

The `hookstest` package provides comprehensive mock implementations for testing MCP (Model Context Protocol) streaming HTTP servers. It uses the options pattern to create flexible and configurable mock capabilities.

## Overview

This package provides:

- `MockHooks`: A configurable implementation of the `hooks.Hooks` interface
- Individual mock capability types for each MCP capability
- Flexible configuration through the options pattern
- Support for testing notification listeners and custom behaviors

## Basic Usage

### Simple Mock Server

```go
// Create a basic mock hooks implementation
mockHooks := hookstest.NewMockHooks()

// Create with tools capability enabled
mockHooks := hookstest.NewMockHooks(
    hookstest.WithEmptyToolsCapability(),
)
```

### Custom Server Configuration

```go
mockHooks := hookstest.NewMockHooks(
    hookstest.WithServerName("my-test-server"),
    hookstest.WithServerVersion("1.2.3"),
    hookstest.WithMockToolsCapability(
        hookstest.WithTools(myTool1, myTool2),
        hookstest.WithToolsChangedSupport(),
    ),
)
```

## Mock Capabilities

### Tools Capability

```go
// Create a tool for testing
testTool := mcp.Tool{
    Name:        "test-tool",
    Description: stringPtr("A test tool"),
    InputSchema: mcp.ToolInputSchema{
        Type: "object",
        Properties: map[string]mcp.SchemaProperty{
            "input": {Type: "string", Description: stringPtr("Test input")},
        },
        Required: []string{"input"},
    },
}

// Configure tools capability
toolsCapability := hookstest.NewMockToolsCapability(
    hookstest.WithTools(testTool),
    hookstest.WithCallToolHandler(func(ctx context.Context, session hooks.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
        return &mcp.CallToolResult{
            Content: []mcp.ContentBlock{{Type: "text", Text: stringPtr("Success!")}},
        }, nil
    }),
    hookstest.WithToolsChangedSupport(),
)

// Use in MockHooks
mockHooks := hookstest.NewMockHooks(
    hookstest.WithToolsCapability(toolsCapability),
)
```

### Resources Capability

```go
testResource := mcp.Resource{
    URI:         "file:///test.txt",
    Name:        "test.txt",
    Description: stringPtr("A test resource"),
    MimeType:    stringPtr("text/plain"),
}

resourcesCapability := hookstest.NewMockResourcesCapability(
    hookstest.WithResources(testResource),
    hookstest.WithResourceContents("file:///test.txt", []mcp.ResourceContents{{
        URI:      "file:///test.txt",
        MimeType: stringPtr("text/plain"),
        Text:     stringPtr("Hello, world!"),
    }}),
    hookstest.WithResourcesListChangedSupport(),
    hookstest.WithResourceUpdatedSupport(),
)
```

### Prompts Capability

```go
testPrompt := mcp.Prompt{
    Name:        "test-prompt",
    Description: stringPtr("A test prompt"),
    Arguments: []mcp.PromptArgument{{
        Name:        "topic",
        Description: stringPtr("The topic to discuss"),
        Required:    boolPtr(true),
    }},
}

promptResult := &mcp.GetPromptResult{
    Messages: []mcp.PromptMessage{{
        Role: mcp.RoleUser,
        Content: []mcp.ContentBlock{{Type: "text", Text: stringPtr("Tell me about {{topic}}")}},
    }},
}

promptsCapability := hookstest.NewMockPromptsCapability(
    hookstest.WithPrompts(testPrompt),
    hookstest.WithPromptResult("test-prompt", promptResult),
    hookstest.WithPromptsListChangedSupport(),
)
```

### Logging Capability

```go
loggingCapability := hookstest.NewMockLoggingCapability(
    hookstest.WithInitialLogLevel(mcp.LoggingLevelDebug),
    hookstest.WithLoggingMessageSupport(),
)

// Test current level
currentLevel := loggingCapability.GetCurrentLevel()

// Send a test log message
err := loggingCapability.SendLogMessage(ctx, mcp.LoggingLevelInfo, "test message", stringPtr("test-logger"))
```

### Completions Capability

```go
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
```

## Testing Notifications

The mock capabilities support testing notification listeners:

```go
toolsCapability := hookstest.NewMockToolsCapability(
    hookstest.WithToolsChangedSupport(),
)

// Register listener (this would normally be done by the framework)
registered, err := toolsCapability.RegisterToolsChangedListener(ctx, session, func(ctx context.Context) error {
    // Handle tools changed notification
    return nil
})

// Trigger the notification in your test
err = toolsCapability.TriggerToolsChanged(ctx)
```

## Helper Functions

The package includes helper functions for creating pointers, which are commonly needed in MCP structures:

```go
func stringPtr(s string) *string { return &s }
func boolPtr(b bool) *bool       { return &b }
func intPtr(i int) *int          { return &i }
```

## Complete Example

```go
func TestMyMCPServer(t *testing.T) {
    ctx := context.Background()

    // Create comprehensive mock hooks
    mockHooks := hookstest.NewMockHooks(
        hookstest.WithServerName("my-server"),
        hookstest.WithMockToolsCapability(
            hookstest.WithTools(mcp.Tool{
                Name: "echo",
                Description: stringPtr("Echo tool"),
                InputSchema: mcp.ToolInputSchema{Type: "object"},
            }),
            hookstest.WithCallToolHandler(func(ctx context.Context, session hooks.Session, req *mcp.CallToolRequestReceived) (*mcp.CallToolResult, error) {
                return &mcp.CallToolResult{
                    Content: []mcp.ContentBlock{{Type: "text", Text: stringPtr("Echo: " + req.Name)}},
                }, nil
            }),
        ),
        hookstest.WithMockResourcesCapability(
            hookstest.WithResources(mcp.Resource{
                URI:  "file:///readme.md",
                Name: "README",
            }),
        ),
        hookstest.WithMockLoggingCapability(
            hookstest.WithLoggingMessageSupport(),
        ),
    )

    // Use mockHooks in your server tests
    // ...
}
```

This provides a comprehensive testing framework for MCP streaming HTTP servers with full control over all capabilities and their behaviors.
