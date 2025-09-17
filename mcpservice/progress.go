package mcpservice

import "context"

// ProgressReporter is a minimal interface for reporting progress of a long-running
// operation. Implementations are transport-specific and injected into the context
// by the transport handlers. Server code can retrieve it from context and call
// Report to emit notifications/progress to the client correlated to the current
// request.
type ProgressReporter interface {
	// Report emits a progress update. Implementations should treat values as
	// opaque and simply forward to the client; server code is responsible for
	// selecting meaningful scales for progress and total. total may be zero or
	// omitted depending on transport implementation.
	Report(ctx context.Context, progress, total float64) error
}

type progressKey struct{}

// WithProgressReporter returns a new context carrying the provided reporter.
func WithProgressReporter(ctx context.Context, pr ProgressReporter) context.Context {
	if pr == nil {
		return ctx
	}
	return context.WithValue(ctx, progressKey{}, pr)
}

// ProgressFrom retrieves a ProgressReporter from the context if present.
func ProgressFrom(ctx context.Context) (ProgressReporter, bool) {
	if v := ctx.Value(progressKey{}); v != nil {
		if pr, ok := v.(ProgressReporter); ok && pr != nil {
			return pr, true
		}
	}
	return nil, false
}
