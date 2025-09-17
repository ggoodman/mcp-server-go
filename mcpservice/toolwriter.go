package mcpservice

import (
	"context"
	"errors"
	"sync"

	"github.com/ggoodman/mcp-server-go/mcp"
)

// ToolResponseWriter allows a tool handler to incrementally compose a
// CallToolResult while optionally emitting progress notifications.
//
// Notes:
// - It is concurrency-safe for use within a single request.
// - Writes after finalization (Result) are ignored and return ErrFinalized.
// - All mutating methods check ctx.Done() and return the context error promptly.
// - SendProgress delegates to the ambient ProgressReporter when present; it is a no-op otherwise.
type ToolResponseWriter interface {
	AppendText(text string) error
	AppendBlocks(blocks ...mcp.ContentBlock) error
	SetError(isError bool)
	SetMeta(key string, v any)
	SendProgress(progress, total float64) error
	// Result finalizes and returns the accumulated result. It is idempotent.
	Result() *mcp.CallToolResult
}

var (
	// ErrFinalized is returned when attempting to write after Result() was called.
	ErrFinalized = errors.New("result already finalized")
)

type toolResponseWriter struct {
	ctx       context.Context
	mu        sync.Mutex
	finalized bool

	blocks  []mcp.ContentBlock
	isError bool
	meta    map[string]any
}

// Ensure toolResponseWriter implements ToolResponseWriter.
var _ ToolResponseWriter = (*toolResponseWriter)(nil)

func newToolResponseWriter(ctx context.Context) *toolResponseWriter {
	return &toolResponseWriter{ctx: ctx, meta: make(map[string]any)}
}

func (w *toolResponseWriter) AppendText(text string) error {
	if text == "" {
		return nil
	}
	return w.AppendBlocks(mcp.ContentBlock{Type: "text", Text: text})
}

func (w *toolResponseWriter) AppendBlocks(blocks ...mcp.ContentBlock) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finalized {
		return ErrFinalized
	}
	if len(blocks) == 0 {
		return nil
	}
	w.blocks = append(w.blocks, blocks...)
	return nil
}

func (w *toolResponseWriter) SetError(isError bool) {
	w.mu.Lock()
	w.isError = isError
	w.mu.Unlock()
}

func (w *toolResponseWriter) SetMeta(key string, v any) {
	if key == "" {
		return
	}
	w.mu.Lock()
	if w.meta == nil {
		w.meta = make(map[string]any)
	}
	w.meta[key] = v
	w.mu.Unlock()
}

func (w *toolResponseWriter) SendProgress(progress, total float64) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if pr, ok := ProgressFrom(w.ctx); ok && pr != nil {
		return pr.Report(w.ctx, progress, total)
	}
	return nil
}

func (w *toolResponseWriter) Result() *mcp.CallToolResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finalized {
		// Return a shallow copy to avoid external mutation.
		return &mcp.CallToolResult{Content: append([]mcp.ContentBlock(nil), w.blocks...), IsError: w.isError, BaseMetadata: mcp.BaseMetadata{Meta: cloneMeta(w.meta)}}
	}
	w.finalized = true
	return &mcp.CallToolResult{Content: append([]mcp.ContentBlock(nil), w.blocks...), IsError: w.isError, BaseMetadata: mcp.BaseMetadata{Meta: cloneMeta(w.meta)}}
}

func cloneMeta(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
