package elicitation

import (
	"reflect"
	"testing"
)

type sample struct {
	Name  string `json:"name" jsonschema:"description=User name"`
	Count int    `json:"count" jsonschema:"minimum=1,maximum=10"`
}

func TestProjectAndDecode(t *testing.T) {
	schema, cp, err := ProjectSchema(reflect.TypeOf(sample{}))
	if err != nil {
		t.Fatalf("project error: %v", err)
	}
	if schema.Type != "object" {
		t.Fatalf("expected object type")
	}
	if len(schema.Properties) != 2 {
		t.Fatalf("expected 2 props, got %d", len(schema.Properties))
	}

	var dst sample
	raw := map[string]any{"name": "alice", "count": 3}
	if err := DecodeForTyped(cp, &dst, raw, true); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if dst.Name != "alice" || dst.Count != 3 {
		t.Fatalf("unexpected decode: %+v", dst)
	}
}

func TestDecodeBounds(t *testing.T) {
	_, cp, err := ProjectSchema(reflect.TypeOf(sample{}))
	if err != nil {
		t.Fatalf("project error: %v", err)
	}
	var dst sample
	if err := DecodeForTyped(cp, &dst, map[string]any{"name": "bob", "count": 0}, true); err == nil {
		t.Fatalf("expected bounds error")
	}
}
