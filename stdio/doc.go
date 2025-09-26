// Package stdio implements a minimal single-connection MCP transport over
// stdin/stdout. It is intended for embedding servers as subprocesses, local
// development, and environments where spawning a child process and piping JSON
// is simpler than running an HTTP server.
//
// Characteristics
//
//	Connection model : 1 process <-> 1 client
//	Auth             : OS user (lightweight implicit principal)
//	Sessions         : Ephemeral; no host abstraction (memory only)
//	Transport        : Line / stream oriented JSON-RPC
//
// Options allow supplying alternate io.Reader / io.Writer or a custom logger.
//
// Example:
//
//	srv := mcpservice.NewServer(
//	    mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "my-stdio-server", Version: "0.1.0"}),
//	    // mcpservice.WithToolsCapability(...), etc.
//	)
//	h := stdio.NewHandler(srv)
//	if err := h.Serve(context.Background()); err != nil { log.Fatal(err) }
//
// For multi-session, horizontally scalable deployments prefer the streaming
// HTTP transport which integrates with session hosts, authentication and
// subscription fan-out.
package stdio
