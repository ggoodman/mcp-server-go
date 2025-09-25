package elicitation

import (
	"encoding/json"
	"reflect"
	"testing"
)

// helper to unmarshal schema JSON for inspection
func extract(root Schema) (map[string]any, error) {
	b, err := root.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func TestBindStruct_Errors(t *testing.T) {
	_, err := BindStruct(nil)
	if err == nil {
		t.Fatalf("expected error for nil pointer")
	}
	v := 5
	_, err = BindStruct(v)
	if err == nil {
		t.Fatalf("expected error for non-pointer")
	}
	_, err = BindStruct(&v)
	if err == nil {
		t.Fatalf("expected error for pointer to non-struct")
	}
}

type simple struct { // unexported -> should be skipped entirely if embedded
	Hidden string `json:"hidden"`
}

type sample struct {
	Name  string  `json:"name" jsonschema:"minLength=1,description=User name"`
	Alias *string `json:"alias" jsonschema:"description=Optional alias"`
	Age   int     `json:"age" jsonschema:"minimum=0,maximum=120,description=Age in years"`
	Skip  string  `json:"-"`
	Kind  string  `json:"kind" jsonschema:"enum=basic|pro,description=Account type"`
	// Unsupported nested struct should be ignored
	Nested simple
}

func TestBindStruct_SchemaShape(t *testing.T) {
	var s sample
	dec, err := BindStruct(&s)
	if err != nil {
		t.Fatalf("BindStruct failed: %v", err)
	}
	sch, err := dec.Schema()
	if err != nil {
		t.Fatalf("Schema() failed: %v", err)
	}
	root, err := extract(sch)
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	if root["type"] != "object" {
		t.Fatalf("expected object root")
	}
	props := root["properties"].(map[string]any)
	// required should include name, age, kind (non-pointers) but not alias
	reqAny, _ := root["required"].([]any)
	reqSet := map[string]struct{}{}
	for _, r := range reqAny {
		reqSet[r.(string)] = struct{}{}
	}
	for _, field := range []string{"name", "age", "kind"} {
		if _, ok := reqSet[field]; !ok {
			t.Fatalf("expected required field %s", field)
		}
	}
	if _, ok := reqSet["alias"]; ok {
		t.Fatalf("alias should not be required")
	}
	// verify skipped & unsupported
	if _, ok := props["Skip"]; ok {
		t.Fatalf("expected Skip to be absent")
	}
	if _, ok := props["Nested"]; ok {
		t.Fatalf("expected Nested to be absent")
	}
	// enum present
	kind := props["kind"].(map[string]any)
	if _, ok := kind["enum"]; !ok {
		t.Fatalf("expected enum on kind")
	}
}

func TestDecode_Success(t *testing.T) {
	var s sample
	dec, _ := BindStruct(&s)
	content := map[string]any{"name": "Alice", "age": float64(30), "kind": "basic"}
	if err := dec.Decode(content, nil); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if s.Name != "Alice" || s.Age != 30 || s.Kind != "basic" {
		t.Fatalf("unexpected struct values: %+v", s)
	}
}

func TestDecode_OptionalPointerAllocated(t *testing.T) {
	var s sample
	dec, _ := BindStruct(&s)
	alias := "Al"
	content := map[string]any{"name": "Alice", "age": float64(30), "kind": "pro", "alias": alias}
	if err := dec.Decode(content, nil); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if s.Alias == nil || *s.Alias != alias {
		t.Fatalf("alias not set correctly")
	}
}

func TestDecode_MissingRequired(t *testing.T) {
	var s sample
	dec, _ := BindStruct(&s)
	if err := dec.Decode(map[string]any{"age": float64(25), "kind": "basic"}, nil); err == nil {
		// name missing
		t.Fatalf("expected error for missing required field")
	}
}

func TestDecode_EnumViolation(t *testing.T) {
	var s sample
	dec, _ := BindStruct(&s)
	err := dec.Decode(map[string]any{"name": "Bob", "age": float64(25), "kind": "enterprise"}, nil)
	if err == nil {
		t.Fatalf("expected enum violation error")
	}
}

func TestDecode_TypeMismatchNoMutation(t *testing.T) {
	var s sample
	dec, _ := BindStruct(&s)
	err := dec.Decode(map[string]any{"name": 42, "age": float64(25), "kind": "basic"}, nil)
	if err == nil {
		t.Fatalf("expected type mismatch error")
	}
	// Ensure struct still zero-valued
	if !reflect.ValueOf(s).IsZero() {
		t.Fatalf("struct should remain zero on failure: %+v", s)
	}
}

func TestFingerprintStable(t *testing.T) {
	var s sample
	dec, _ := BindStruct(&s)
	sch1, _ := dec.Schema()
	sch2, _ := dec.Schema()
	if sch1.Fingerprint() != sch2.Fingerprint() {
		t.Fatalf("fingerprint should be stable across calls")
	}
}

func TestEmptyStruct(t *testing.T) {
	var x struct{}
	dec, err := BindStruct(&x)
	if err != nil {
		t.Fatalf("BindStruct empty struct: %v", err)
	}
	sch, _ := dec.Schema()
	root, _ := extract(sch)
	if _, ok := root["required"]; ok {
		t.Fatalf("expected no required array for empty struct")
	}
	props := root["properties"].(map[string]any)
	if len(props) != 0 {
		t.Fatalf("expected zero properties")
	}
}

func TestNumericCoercion(t *testing.T) {
	var s struct {
		Count int `json:"count"`
	}
	dec, _ := BindStruct(&s)
	if err := dec.Decode(map[string]any{"count": float64(5)}, nil); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if s.Count != 5 {
		t.Fatalf("expected 5 got %d", s.Count)
	}
}
