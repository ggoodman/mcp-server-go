package stdio

import (
	"io"
	"log/slog"
)

// Option customizes a Handler.
type Option func(*Handler)

// WithIO sets the reader and writer for the handler.
func WithIO(r io.Reader, w io.Writer) Option {
	return func(h *Handler) {
		if r != nil {
			h.r = r
		}
		if w != nil {
			h.w = w
		}
	}
}

// WithReader overrides the input stream.
func WithReader(r io.Reader) Option {
	return func(h *Handler) {
		if r != nil {
			h.r = r
		}
	}
}

// WithWriter overrides the output stream.
func WithWriter(w io.Writer) Option {
	return func(h *Handler) {
		if w != nil {
			h.w = w
		}
	}
}

// WithLogger overrides the logger.
func WithLogger(l *slog.Logger) Option {
	return func(h *Handler) {
		if l != nil {
			h.l = l
		}
	}
}

// WithUserProvider overrides the user provider used for authless identification.
func WithUserProvider(up UserProvider) Option {
	return func(h *Handler) {
		if up != nil {
			h.userProvider = up
		}
	}
}
