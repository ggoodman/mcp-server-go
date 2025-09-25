package elicitation

import (
	"encoding/json"
	"testing"
)

func TestBuilder_BasicSchema(t *testing.T) {
	b := NewBuilder().
		String("name", Required(), Description("User name"), MinLength(1)).
		Number("score", Optional(), Minimum(0), Maximum(100)).
		EnumString("tier", []string{"free","pro"}, Optional())

	sch, err := b.Build()
	if err != nil { t.Fatalf("Build failed: %v", err) }

	js, _ := sch.MarshalJSON()
	var m map[string]any
	_ = json.Unmarshal(js, &m)
	if m["type"] != "object" { t.Fatalf("expected object type") }
	props := m["properties"].(map[string]any)
	if _, ok := props["name"]; !ok { t.Fatalf("missing name prop") }
	if _, ok := props["score"]; !ok { t.Fatalf("missing score prop") }
	if _, ok := props["tier"]; !ok { t.Fatalf("missing tier prop") }
}

func TestBuilder_RequiredSet(t *testing.T) {
	b := NewBuilder().String("a", Required()).String("b", Optional())
	sch, _ := b.Build()
	js, _ := sch.MarshalJSON()
	var m map[string]any
	_ = json.Unmarshal(js, &m)
	req := toStringSet(m["required"].([]any))
	if _, ok := req["a"]; !ok { t.Fatalf("a must be required") }
	if _, ok := req["b"]; ok { t.Fatalf("b must not be required") }
}

func toStringSet(arr []any) map[string]struct{} { s := map[string]struct{}{}; for _, v := range arr { s[v.(string)] = struct{}{} }; return s }

func TestBuilder_BindAndDecode(t *testing.T) {
	b := NewBuilder().String("name", Required()).Number("age", Optional())
	var dst map[string]any
	dec := b.MustBind(&dst)
	if err := dec.Decode(map[string]any{"name":"Alice","age":float64(33)}, nil); err != nil { t.Fatalf("decode failed: %v", err) }
	if dst["name"].(string) != "Alice" { t.Fatalf("unexpected dst %v", dst) }
}

func TestBuilder_DecodeMissingRequired(t *testing.T) {
	b := NewBuilder().String("name", Required())
	var dst map[string]any
	dec := b.MustBind(&dst)
	if err := dec.Decode(map[string]any{}, nil); err == nil { t.Fatalf("expected missing required error") }
}

func TestBuilder_DecodeEnum(t *testing.T) {
	b := NewBuilder().EnumString("tier", []string{"free","pro"}, Required())
	var dst map[string]any
	dec := b.MustBind(&dst)
	if err := dec.Decode(map[string]any{"tier":"pro"}, nil); err != nil { t.Fatalf("decode failed: %v", err) }
	if err := dec.Decode(map[string]any{"tier":"enterprise"}, nil); err == nil { t.Fatalf("expected enum error") }
}

func TestBuilder_FingerprintStable(t *testing.T) {
	b := NewBuilder().String("x", Required())
	sch1, _ := b.Build()
	// Rebuilding after Build() should error if reused
	if _, err := b.Build(); err == nil { t.Fatalf("expected reuse error") }
	fp1 := sch1.Fingerprint()
	// New identical builder yields different instance same canonical JSON -> may or may not match depending on ordering rules
	b2 := NewBuilder().String("x", Required())
	sch2, _ := b2.Build()
	fp2 := sch2.Fingerprint()
	if fp1 != fp2 { t.Fatalf("expected identical fingerprints for identical schema") }
}
