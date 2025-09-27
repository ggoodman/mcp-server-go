package prompts_static

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// New constructs a server with a static prompts capability. It registers a single
// "greeting" prompt that renders a simple assistant message using a required
// "name" argument.
func New() mcpservice.ServerCapabilities {
	greeting := mcp.Prompt{
		Name:        "greeting",
		Description: "Say hello to a user by name",
		Arguments: []mcp.PromptArgument{
			{Name: "name", Description: "The user's name", Required: true},
		},
	}

	// Handler parses the name argument and returns a single assistant message.
	greetingHandler := func(_ sessions.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error) {
		// Defensive checks
		if req == nil || req.Name == "" {
			return nil, fmt.Errorf("invalid prompt request")
		}
		var name string
		if raw, ok := req.Arguments["name"]; ok && len(raw) > 0 {
			if err := json.Unmarshal(raw, &name); err != nil || name == "" {
				return nil, fmt.Errorf("invalid 'name' argument")
			}
		} else {
			return nil, fmt.Errorf("missing required 'name' argument")
		}
		return &mcp.GetPromptResult{
			Description: "A friendly greeting",
			Messages: []mcp.PromptMessage{
				{
					Role: mcp.RoleAssistant,
					Content: []mcp.ContentBlock{{
						Type: "text",
						Text: "Hello, " + name + "!",
					}},
				},
			},
		}, nil
	}

	// Wrap handler into PromptsContainer
	sp := mcpservice.NewPromptsContainer(
		mcpservice.StaticPrompt{Descriptor: greeting, Handler: func(_ context.Context, s sessions.Session, req *mcp.GetPromptRequestReceived) (*mcp.GetPromptResult, error) {
			return greetingHandler(s, req)
		}},
	)

	return mcpservice.NewServer(
		mcpservice.WithServerInfo(mcpservice.StaticServerInfo("examples-prompts-static", "0.1.0")),
		mcpservice.WithPromptsCapability(sp),
	)
}
