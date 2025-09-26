// Package sampling provides lightweight helpers for constructing MCP
// sampling/createMessage requests. It focuses on ergonomics when building
// chat style message arrays and configuring optional generation parameters.
//
// The core wire type lives in package mcp (CreateMessageRequest). This
// package layers a small builder pattern over it:
//   - Convenience constructors for single-block user / assistant messages
//   - Functional options for system prompt, temperature, max tokens, stop sequences
//   - Validation helper for preflight sanity checks before sending
//
// Example:
//
//	msgs := []mcp.SamplingMessage{
//	    sampling.UserText("Summarize this repository"),
//	}
//	req := sampling.NewCreateMessage(
//	    msgs,
//	    sampling.WithSystemPrompt("You are a terse summarizer."),
//	    sampling.WithTemperature(0.2),
//	    sampling.WithMaxTokens(256),
//	)
//	if err := sampling.ValidateCreateMessage(req); err != nil { return err }
//	// send via your session's SamplingCapability
//
// The helpers are intentionally minimal; they do not attempt to model provider
// specific parameters beyond those in the protocol. Applications are free to
// extend CreateMessageRequest.Metadata for vendor-specific hints.
package sampling
