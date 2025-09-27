package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/sampling"
)

func fail(w mcpservice.ToolResponseWriter, msg string) error {
	w.SetError(true)
	w.AppendText(msg)
	return nil
}

// NewExampleServer constructs the MCP server with tools and capabilities used in the README example.
// It isolates all MCP-specific concerns from the HTTP wiring so the example is easier to reason about.
func NewExampleServer() mcpservice.ServerCapabilities {
	// TranslateArgs defines the input to the translate tool.
	type TranslateArgs struct {
		Text string `json:"text" jsonschema:"minLength=1,description=Text to translate"`
	}

	translate := mcpservice.NewTool(
		"translate",
		func(ctx context.Context, sess sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[TranslateArgs]) error {
			a := r.Args()

			el, ok := sess.GetElicitationCapability()
			if !ok {
				return fail(w, "Elicitation capability not available in this session.")
			}

			sp, ok := sess.GetSamplingCapability()
			if !ok {
				return fail(w, "Sampling capability not available in this session.")
			}

			var elic struct {
				Language string `json:"language" jsonschema:"minLength=1,description=Target language for translation (e.g. French, Spanish)"`
			}

			action, err := el.Elicit(ctx, "Which language should I translate to?", &elic)
			if err != nil {
				return fail(w, "Elicitation error: "+err.Error())
			}
			if action != sessions.ElicitActionAccept {
				return fail(w, "Elicitation not accepted.")
			}
			lang := elic.Language
			if lang == "" {
				return fail(w, "Elicitation did not return a valid language.")
			}

			sres, err := sp.CreateMessage(ctx,
				"You're a helpful assistant that translates text into the requested language. You respond with the translated message and nothing else.",
				// Provide user message conveying intent + input.
				sampling.UserText("Translate the following text to "+lang+"\n"+a.Text),
			)
			if err != nil {
				return fail(w, "Sampling error: "+err.Error())
			}

			if txt, ok := sres.Message.Content.(sampling.Text); ok {
				_ = w.AppendBlocks(mcp.ContentBlock{Type: mcp.ContentTypeText, Text: txt.Text})
			}
			return nil
		},
		mcpservice.WithToolDescription("Translate text to a target language."),
	)

	tools := mcpservice.NewToolsContainer(translate)

	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcpservice.StaticServerInfo("my-mcp", "1.0.0")),
		mcpservice.WithToolsCapability(tools),
	)

	return server
}

// Logger helper if a caller doesn't want to provide one explicitly.
func defaultLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
