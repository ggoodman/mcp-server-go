package mcpserver

// Page represents a single page of results with an optional cursor for fetching
// the next page. It is a generic helper intended for server capability methods
// that return paginated data.
//
// Items is never nil; NewPage normalizes nil input to an empty slice for
// ergonomics at call sites.
type Page[T any] struct {
	Items      []T
	NextCursor *string
}

// PageOption configures a Page constructed via NewPage.
type PageOption[T any] func(*Page[T])

// WithNextCursor sets the next cursor on the Page to indicate that more
// results are available.
func WithNextCursor[T any](cursor string) PageOption[T] {
	return func(p *Page[T]) {
		p.NextCursor = &cursor
	}
}

// NewPage constructs a Page with the provided items and optional configuration
// options. If items is nil, it will be replaced with an empty slice.
func NewPage[T any](items []T, opts ...PageOption[T]) Page[T] {
	if items == nil {
		items = make([]T, 0)
	}
	p := Page[T]{
		Items:      items,
		NextCursor: nil,
	}
	for _, opt := range opts {
		opt(&p)
	}
	return p
}
