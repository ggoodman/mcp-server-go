package resources_fs

import (
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpserver"
)

// New constructs a server that exposes a directory of the local filesystem as
// MCP resources. It demonstrates the dynamic resources capability using the
// FSResources implementation.
func New(root string) mcpserver.ServerCapabilities {
	fsCap := mcpserver.NewFSResources(
		mcpserver.WithOSDir(root),
		mcpserver.WithBaseURI("fs://example"),
		mcpserver.WithFSPageSize(100),
	)

	return mcpserver.NewServer(
		mcpserver.WithServerInfo(mcp.ImplementationInfo{Name: "examples-resources-fs", Version: "0.1.0"}),
		mcpserver.WithResourcesCapability(fsCap),
	)
}
