package sessions

import (
	"context"
	"fmt"
	"reflect"

	internalelicitation "github.com/ggoodman/mcp-server-go/internal/elicitation"
	"github.com/ggoodman/mcp-server-go/mcp"
)

// ElicitTypeOption configures typed elicitation behavior for the helper.
type ElicitTypeOption func(*elicitTypeOptions)

type elicitTypeOptions struct {
	strictKeys bool
}

// WithStrictKeys causes decoding to fail if the client returns keys not present in the projected schema.
func WithStrictKeys() ElicitTypeOption { return func(o *elicitTypeOptions) { o.strictKeys = true } }

// TypedElicitResult is an alias to the protocol-level generic result structure for convenience.
// Deprecated: use mcp.TypedElicitResult[T] directly; this remains for potential future ergonomic additions.
type TypedElicitResult[T any] = mcp.TypedElicitResult[T]

// ElicitType derives an elicitation schema from the shape of T, sends the request
// via the raw capability, and decodes the response into a new *T. This is a
// skeleton pending projection + decoding implementation.
func ElicitType[T any](cap ElicitationCapability, ctx context.Context, message string, opts ...ElicitTypeOption) (*mcp.TypedElicitResult[T], error) {
	if cap == nil {
		return nil, fmt.Errorf("elicitation: capability unavailable")
	}
	var cfg elicitTypeOptions
	for _, o := range opts {
		o(&cfg)
	}

	var zero T
	t := reflect.TypeOf(zero)
	if t == nil {
		t = reflect.TypeOf((*T)(nil)).Elem()
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	schema, cp, err := internalelicitation.ProjectSchema(t)
	if err != nil {
		return nil, err
	}

	res, err := cap.Elicit(ctx, &mcp.ElicitRequest{Message: message, RequestedSchema: *schema})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("elicitation: nil result")
	}

	ptr := new(T)
	if err := internalelicitation.DecodeForTyped(cp, ptr, res.Content, cfg.strictKeys); err != nil {
		return nil, err
	}

	return &mcp.TypedElicitResult[T]{
		Action: res.Action,
		Value:  ptr,
		Raw:    res.Content,
	}, nil
}
