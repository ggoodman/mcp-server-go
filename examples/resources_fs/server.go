package resources_fs

import (
	"github.com/ggoodman/mcp-server-go/mcpservice"
)

// New constructs a server that exposes a directory of the local filesystem as
// MCP resources. It demonstrates the dynamic resources capability using the
// FSResources implementation.
func New(root string) mcpservice.ServerCapabilities {
	fsCap := mcpservice.NewFSResources(
		mcpservice.WithOSDir(root),
		mcpservice.WithBaseURI("fs://example"),
		mcpservice.WithFSPageSize(100),
	)

	return mcpservice.NewServer(
		mcpservice.WithServerInfo(mcpservice.StaticServerInfo("examples-resources-fs", "0.1.0")),
		mcpservice.WithResourcesCapability(fsCap),
	)
}
