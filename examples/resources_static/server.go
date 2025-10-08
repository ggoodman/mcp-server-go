package resources_static

import (
	"github.com/ggoodman/mcp-server-go/mcpservice"
)

// New constructs a server with a static resources capability: two small text resources
// and their contents. Useful for listing and reading examples.
func New() mcpservice.ServerCapabilities {
	static := mcpservice.NewResourcesContainer()
	// Populate via upserts for clarity
	static.UpsertResource(mcpservice.TextResource("res://hello.txt", "hello",
		mcpservice.WithName("hello.txt"),
		mcpservice.WithMimeType("text/plain"),
	))
	static.UpsertResource(mcpservice.TextResource("res://readme.md", "# Readme\nThis is a test.",
		mcpservice.WithName("readme.md"),
		mcpservice.WithMimeType("text/markdown"),
	))
	return mcpservice.NewServer(
		mcpservice.WithServerInfo(mcpservice.StaticServerInfo("examples-resources-static", "0.1.0")),
		mcpservice.WithResourcesCapability(static),
	)
}
