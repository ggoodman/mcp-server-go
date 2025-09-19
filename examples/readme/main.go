package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/redishost"
	"github.com/ggoodman/mcp-server-go/streaminghttp"
)

type TranslateArgs struct {
	Text string `json:"text" jsonschema:"minLength=1,description=Text to translate"`
}

func main() {
	ctx := context.Background()

	publicEndpoint := os.Getenv("MCP_PUBLIC_ENDPOINT") // e.g. https://mcp.example.com/mcp
	issuer := os.Getenv("OIDC_ISSUER")                 // your OAuth/OIDC issuer URL

	// 1) Session host for horizontal scale (Redis)
	host, err := redishost.New(os.Getenv("REDIS_ADDR"))
	if err != nil {
		panic(err)
	}
	defer host.Close()

	// 2) Typed tool with strict input schema by default
	translate := mcpservice.NewTool[TranslateArgs](
		"translate",
		func(ctx context.Context, sess sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[TranslateArgs]) error {
			a := r.Args()

			el, ok := sess.GetElicitationCapability()
			if !ok {
				w.SetError(true)
				w.AppendText("Elicitation capability not available in this session.")
				return nil
			}

			sp, ok := sess.GetSamplingCapability()
			if !ok {
				w.SetError(true)
				w.AppendText("Sampling capability not available in this session.")
				return nil
			}

			res, err := el.Elicit(ctx, &mcp.ElicitRequest{
				Prompt: "Which language should I translate to?",
				Schema: mcp.ElicitationSchema{
					Type: "object",
					Properties: map[string]mcp.PrimitiveSchemaDefinition{
						"language": {
							Type:        "string",
							Description: "Target language for translation (e.g. 'French', 'Spanish')",
						},
					},
					Required: []string{"language"},
				},
			})
			if err != nil {
				w.SetError(true)
				w.AppendText("Elicitation error: " + err.Error())
				return nil
			}

			lang, ok := res.Values["language"].(string)
			if !ok || lang == "" {
				w.SetError(true)
				w.AppendText("Elicitation did not return a valid language.")
				return nil
			}

			sres, err := sp.CreateMessage(ctx, &mcp.CreateMessageRequest{
				Messages: []mcp.SamplingMessage{
					{
						Role:    "user",
						Content: []mcp.ContentBlock{{Type: "text", Text: "Translate the following text to " + lang + ": " + a.Text}},
					},
				},
				SystemPrompt: "You're a helpful assistant that translates text into the requested language. You respond with the translated message and nothing else.",
			})
			if err != nil {
				w.SetError(true)
				w.AppendText("Sampling error: " + err.Error())
				return nil
			}

			_ = w.AppendBlocks(sres.Content)
			return nil
		},
		mcpservice.WithToolDescription("Translate text to a target language."),
	)
	tools := mcpservice.NewStaticTools(translate)

	// 3) Server capabilities
	server := mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "my-mcp", Version: "1.0.0"}),
		mcpservice.WithToolsOptions(mcpservice.WithStaticToolsContainer(tools)),
	)

	// 4) OAuth2/OIDC JWT access token validation (RFC 9068)
	authenticator, err := auth.NewFromDiscovery(
		ctx,
		issuer,
		auth.WithExpectedAudience(publicEndpoint), // audience check (use your public MCP endpoint)
		auth.WithLeeway(2*time.Minute),
	)
	if err != nil {
		panic(err)
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// 5) Drop-in handler
	h, err := streaminghttp.New(
		ctx,
		publicEndpoint,
		host,
		server,
		authenticator,
		streaminghttp.WithServerName("My MCP Server"),
		streaminghttp.WithLogger(log),
		streaminghttp.WithAuthorizationServerDiscovery(issuer),
	)
	if err != nil {
		panic(err)
	}

	// 6) Serve
	http.ListenAndServe("127.0.0.1:8080", h)
}
