package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/redishost"
	"github.com/ggoodman/mcp-server-go/signing"
	"github.com/ggoodman/mcp-server-go/streaminghttp"
)

type TranslateArgs struct {
	Text string `json:"text" jsonschema:"minLength=1,description=Text to translate"`
	To   string `json:"to"   jsonschema:"enum=en,enum=fr,enum=es,description=Target language (ISO 639-1)"`
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
		func(ctx context.Context, s sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[TranslateArgs]) error {
			a := r.Args()

			sc, ok := s.GetSamplingCapability()
			if !ok {
				w.SetError(true)
				w.AppendText("Sampling capability required")
				return nil
			}

			res, err := sc.CreateMessage(ctx, &mcp.CreateMessageRequest{
				Messages: []mcp.SamplingMessage{{
					Role: "user", Content: []mcp.ContentBlock{
						{
							Type: "text",
							Text: fmt.Sprintf("Translate the string, '%s' to the language '%s'", a.Text, a.To),
						},
					},
				}},
				SystemPrompt:  "You are an expert translator. Respond with only the translated text.",
				MaxTokens:     100,
				Temperature:   0.8,
				StopSequences: []string{"\n"},
			})
			if err != nil {
				return err
			}

			w.AppendBlocks(res.Content)

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

	// 5) Session signer (Ed25519) – production should supply stable keys shared across nodes.
	signer, err := signing.NewEphemeralMemoryJWS()
	if err != nil {
		panic(err)
	}

	// 6) Drop-in handler
	h, err := streaminghttp.New(
		ctx,
		publicEndpoint,
		host,
		server,
		authenticator,
		streaminghttp.WithServerName("My MCP Server"),
		streaminghttp.WithLogger(log),
		streaminghttp.WithAuthorizationServerDiscovery(issuer),
		streaminghttp.WithSessionSigner(signer, issuer),
	)
	if err != nil {
		panic(err)
	}

	// 7) Serve
	http.ListenAndServe("127.0.0.1:8080", h)
}
