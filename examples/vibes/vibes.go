package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/sampling"
	"github.com/ggoodman/mcp-server-go/stdio"
)

// Minimal args: none needed to start the interaction.
type VibeArgs struct{}

type VibePrompt struct {
	Phrase string `json:"phrase" jsonschema:"minLength=3,description=How are you feeling?,title=Vibe"`
}

func vibeCheck(ctx context.Context, s sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[VibeArgs]) error {
	el, ok := s.GetElicitationCapability()
	if !ok {
		return fmt.Errorf("elicitation capability not available in this session")
	}

	var prompt VibePrompt

	// Below, the reference to prompt both documents the expected response shape
	// and populates it when the user accepts the elicitation.
	action, err := el.Elicit(ctx, "What's the vibe?", &prompt)
	if err != nil {
		return err
	}
	if action != sessions.ElicitActionAccept {
		w.AppendText("the user is not feeling it")
		w.SetError(true)
		return nil
	}

	samp, ok := s.GetSamplingCapability()
	if !ok {
		return fmt.Errorf("sampling capability not available in this session")
	}

	// Sample host LLM for a single whimsical word (new ergonomic API).
	res, err := samp.CreateMessage(ctx,
		"Respond with short phrase, capturing the emotional vibe of the submitted message with a touch of whimsy.",
		sampling.UserText(prompt.Phrase),
		sampling.WithMaxTokens(50),
	)
	if err != nil {
		return err
	}

	w.AppendBlocks(res.Message.Content.AsContentBlock())

	if txt, ok := res.Message.Content.(sampling.Text); ok {
		w.AppendText(txt.Text)
	}

	return nil
}

func main() {
	tools := mcpservice.NewToolsContainer(
		mcpservice.NewTool("vibe_check", vibeCheck, mcpservice.WithToolDescription("Herein lies the answer when the question is vibe.")),
	)

	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcpservice.StaticServerInfo("vibe-check-demo", "0.0.1")),
		mcpservice.WithToolsCapability(tools),
		mcpservice.WithInstructions(mcpservice.StaticInstructions("Your finger is on the pulse of the inter-webs. You can feel it. You can help others feel it too.")),
	)

	h := stdio.NewHandler(server)
	if err := h.Serve(context.Background()); err != nil {
		log.Fatal(err)
	}
}
