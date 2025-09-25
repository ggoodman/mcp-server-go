package elicitation

import (
	"reflect"
	"testing"
)

type typedTarget struct {
	Name  string  `json:"name"`
	Alias *string `json:"alias"`
	Age   int     `json:"age"`
	Kind  string  `json:"kind"`
	Flag  bool    `json:"flag"`
	Skip  string  // unmatched; should be ignored
}

func TestBuilder_TypedDecodeSuccess(t *testing.T) {
	b := NewBuilder().
		String("name", Required()).
		String("alias", Optional()).
		Number("age", Required(), Minimum(0)).
		EnumString("kind", []string{"basic","pro"}, Required()).
		Boolean("flag", Optional())

	var tgt typedTarget
	dec := b.MustBindStruct(&tgt)
	aliasVal := "Al"
	content := map[string]any{"name":"Alice","alias":aliasVal,"age":float64(42),"kind":"pro","flag":true}
	if err := dec.Decode(content, nil); err != nil { t.Fatalf("decode failed: %v", err) }
	if tgt.Name != "Alice" || tgt.Age != 42 || tgt.Kind != "pro" || tgt.Flag != true { t.Fatalf("unexpected struct %+v", tgt) }
	if tgt.Alias == nil || *tgt.Alias != aliasVal { t.Fatalf("alias not set") }
}

func TestBuilder_TypedMissingRequired(t *testing.T) {
	b := NewBuilder().String("name", Required()).Number("age", Required())
	var tgt typedTarget
	dec := b.MustBindStruct(&tgt)
	if err := dec.Decode(map[string]any{"name":"Bob"}, nil); err == nil { t.Fatalf("expected missing required age error") }
}

func TestBuilder_TypedEnumViolation(t *testing.T) {
	b := NewBuilder().EnumString("kind", []string{"basic","pro"}, Required())
	var tgt typedTarget
	dec := b.MustBindStruct(&tgt)
	if err := dec.Decode(map[string]any{"kind":"enterprise"}, nil); err == nil { t.Fatalf("expected enum violation") }
}

func TestBuilder_TypedNumericCoercion(t *testing.T) {
	b := NewBuilder().Number("age", Required())
	var tgt typedTarget
	dec := b.MustBindStruct(&tgt)
	if err := dec.Decode(map[string]any{"age":float64(7),"name":""}, nil); err != nil { t.Fatalf("decode failed: %v", err) }
	if tgt.Age != 7 { t.Fatalf("expected age 7 got %d", tgt.Age) }
}

func TestBuilder_TypedNoPartialMutation(t *testing.T) {
	b := NewBuilder().String("name", Required()).Number("age", Required())
	var tgt typedTarget
	dec := b.MustBindStruct(&tgt)
	err := dec.Decode(map[string]any{"name":42,"age":float64(8)}, nil)
	if err == nil { t.Fatalf("expected type error") }
	if !reflect.ValueOf(tgt).IsZero() { t.Fatalf("tgt should remain zero on failure: %+v", tgt) }
}

func TestBuilder_TypedOptionalUnmappedProperty(t *testing.T) {
	// property that won't exist in struct -> optional should be fine
	b := NewBuilder().String("ghost", Optional())
	var tgt typedTarget
	dec := b.MustBindStruct(&tgt)
	if err := dec.Decode(map[string]any{"ghost":"value"}, nil); err != nil { t.Fatalf("decode failed: %v", err) }
}

func TestBuilder_TypedRequiredUnmappedFails(t *testing.T) {
	b := NewBuilder().String("ghost", Required())
	var tgt typedTarget
	_, err := b.BindStruct(&tgt)
	if err == nil { t.Fatalf("expected bind error for required unmapped property") }
}
