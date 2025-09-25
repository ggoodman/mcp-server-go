package resources_static

import (
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
)

// New constructs a server with a static resources capability: two small text resources
// and their contents. Useful for listing and reading examples.
func New() mcpservice.ServerCapabilities {
	res := []mcp.Resource{
		{URI: "res://hello.txt", Name: "hello.txt", MimeType: "text/plain"},
		{URI: "res://readme.md", Name: "readme.md", MimeType: "text/markdown"},
	}
	contents := map[string][]mcp.ResourceContents{
		"res://hello.txt": {{URI: "res://hello.txt", MimeType: "text/plain", Text: "hello"}},
		"res://readme.md": {{URI: "res://readme.md", MimeType: "text/markdown", Text: "# Readme\nThis is a test."}},
	}

	static := mcpservice.NewResourcesContainer(res, nil, contents)

	return mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "examples-resources-static", Version: "0.1.0"}),
		mcpservice.WithResourcesCapability(static),
	)
}
