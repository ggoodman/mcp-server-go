// Package stdio provides a minimal MCP transport over stdin/stdout.
//
// Overview
//   - Single-connection transport suitable for spawning an MCP server as a subprocess
//   - Defaults to wiring to os.Stdin and os.Stdout
//   - Optionally accepts custom io.Reader/io.Writer via the functional options pattern
//   - Uses the current OS user as the authenticated principal by default
//
// The stdio transport is intentionally lightweight: it avoids HTTP, sessions hosts,
// and authorization discovery/metadata. It is ideal for local development and for
// servers embedded inside other processes that want to speak MCP over pipes.
//
// Example
//
//	package main
//
//	import (
//	    "context"
//	    "log"
//
//	    "github.com/ggoodman/mcp-server-go/mcp"
//	    "github.com/ggoodman/mcp-server-go/mcpservice"
//	    "github.com/ggoodman/mcp-server-go/stdio"
//	)
//
//	func main() {
//	    // Describe your server capabilities as usual.
//	    server := mcpservice.NewServer(
//	        mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "my-stdio-server", Version: "0.1.0"}),
//	        // mcpservice.WithToolsOptions(...), mcpservice.WithResourcesOptions(...), etc.
//	    )
//
//	    h := stdio.NewHandler(server)
//
//	    if err := h.Serve(context.Background()); err != nil {
//	        log.Fatal(err)
//	    }
//	}
package stdio
