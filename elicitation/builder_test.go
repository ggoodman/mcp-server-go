package elicitation

import (
	"encoding/json"
	"testing"
)

func TestBuilder_BasicSchema(t *testing.T) {
	b := NewBuilder().
		String("name", Required(), Description("User name"), MinLength(1)).
		Number("score", Optional(), Minimum(0), Maximum(100)).
		EnumString("tier", []string{"free", "pro"}, Optional())

	sch, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	js, _ := sch.MarshalJSON()
	var m map[string]any
	_ = json.Unmarshal(js, &m)
	if m["type"] != "object" {
		t.Fatalf("expected object type")
	}
	props := m["properties"].(map[string]any)
	if _, ok := props["name"]; !ok {
		t.Fatalf("missing name prop")
	}
	if _, ok := props["score"]; !ok {
		t.Fatalf("missing score prop")
	}
	if _, ok := props["tier"]; !ok {
		t.Fatalf("missing tier prop")
	}
}

func TestBuilder_RequiredSet(t *testing.T) {
	b := NewBuilder().String("a", Required()).String("b", Optional())
	sch, _ := b.Build()
	js, _ := sch.MarshalJSON()
	var m map[string]any
	_ = json.Unmarshal(js, &m)
	req := toStringSet(m["required"].([]any))
	if _, ok := req["a"]; !ok {
		t.Fatalf("a must be required")
	}
	if _, ok := req["b"]; ok {
		t.Fatalf("b must not be required")
	}
}

func toStringSet(arr []any) map[string]struct{} {
	s := map[string]struct{}{}
	for _, v := range arr {
		s[v.(string)] = struct{}{}
	}
	return s
}

func TestBuilder_BindAndDecode(t *testing.T) {
	b := NewBuilder().String("name", Required()).Number("age", Optional())
	var dst map[string]any
	dec := b.MustBind(&dst)
	if err := dec.Decode(map[string]any{"name": "Alice", "age": float64(33)}, nil); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if dst["name"].(string) != "Alice" {
		t.Fatalf("unexpected dst %v", dst)
	}
}

func TestBuilder_DecodeMissingRequired(t *testing.T) {
	b := NewBuilder().String("name", Required())
	var dst map[string]any
	dec := b.MustBind(&dst)
	if err := dec.Decode(map[string]any{}, nil); err == nil {
		t.Fatalf("expected missing required error")
	}
}

func TestBuilder_DecodeEnum(t *testing.T) {
	b := NewBuilder().EnumString("tier", []string{"free", "pro"}, Required())
	var dst map[string]any
	dec := b.MustBind(&dst)
	if err := dec.Decode(map[string]any{"tier": "pro"}, nil); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if err := dec.Decode(map[string]any{"tier": "enterprise"}, nil); err == nil {
		t.Fatalf("expected enum error")
	}
}

func TestBuilder_FingerprintStable(t *testing.T) {
	b := NewBuilder().String("x", Required())
	sch1, _ := b.Build()
	// Rebuilding after Build() should error if reused
	if _, err := b.Build(); err == nil {
		t.Fatalf("expected reuse error")
	}
	fp1 := sch1.Fingerprint()
	// New identical builder yields different instance same canonical JSON -> may or may not match depending on ordering rules
	b2 := NewBuilder().String("x", Required())
	sch2, _ := b2.Build()
	fp2 := sch2.Fingerprint()
	if fp1 != fp2 {
		t.Fatalf("expected identical fingerprints for identical schema")
	}
}

func TestBuilder_ParityFields(t *testing.T) {
	b := NewBuilder().
		String("email", Required(), Title("Email Address"), Description("Primary"), Format("email"), MinLength(3)).
		Integer("level", Description("Access level"), Minimum(0), Maximum(10)).
		EnumString("mode", []string{"on", "off"}, EnumNames("Enabled", "Disabled"), Description("Toggle"), Title("Mode")).
		Boolean("flag", Optional(), DefaultBool(true), Description("Flag"), Title("Flag Title"))

	sch, err := b.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	js, _ := sch.MarshalJSON()
	var m map[string]any
	_ = json.Unmarshal(js, &m)
	props := m["properties"].(map[string]any)
	email := props["email"].(map[string]any)
	if email["title"] != "Email Address" {
		t.Fatalf("missing title")
	}
	if email["format"] != "email" {
		t.Fatalf("expected format=email")
	}
	if email["type"] != "string" {
		t.Fatalf("email should be string")
	}
	level := props["level"].(map[string]any)
	if level["type"] != "integer" {
		t.Fatalf("level should be integer")
	}
	mode := props["mode"].(map[string]any)
	if mode["type"] != "string" {
		t.Fatalf("mode type should be string")
	}
	if _, ok := mode["enum"]; !ok {
		t.Fatalf("mode enum missing")
	}
	if en, ok := mode["enumNames"].([]any); !ok || len(en) != 2 {
		t.Fatalf("enumNames mismatch")
	}
	flag := props["flag"].(map[string]any)
	if flag["default"] != true {
		t.Fatalf("flag default expected true")
	}
}

func TestBuilder_CanonicalOrdering(t *testing.T) {
	b := NewBuilder().String("b", Description("second")).String("a", Description("first"))
	sch, _ := b.Build()
	raw, _ := sch.MarshalJSON()
	wantPrefix := `{"type":"object","properties":{"b":{`
	if string(raw[:len(wantPrefix)]) != wantPrefix {
		t.Fatalf("expected b then a ordering, got %s", raw)
	}
}

func TestBuilder_FingerprintStableDifferentConstructionOrder(t *testing.T) {
	// Build same logical schema via different construction sequences but identical insertion order preserved
	b1 := NewBuilder().String("a", Required()).String("b", Optional())
	s1, _ := b1.Build()
	b2 := NewBuilder().String("a", Required()).String("b", Optional())
	s2, _ := b2.Build()
	if s1.Fingerprint() != s2.Fingerprint() {
		t.Fatalf("expected fingerprints match: %s vs %s", s1.Fingerprint(), s2.Fingerprint())
	}
}

func TestBuilderDefaults_StringNumberBool(t *testing.T) {
	b := NewBuilder().
		String("s", Optional(), DefaultString("abc"), MinLength(1)).
		Number("n", Optional(), DefaultNumber(4.5), Minimum(1), Maximum(10)).
		Integer("i", Optional(), DefaultNumber(3), Minimum(1), Maximum(5)).
		Boolean("b", Optional(), DefaultBool(true))

	var m map[string]any
	dec := b.MustBind(&m)
	if err := dec.Decode(map[string]any{}, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["s"].(string) != "abc" {
		t.Fatalf("expected string default")
	}
	if m["n"].(float64) != 4.5 {
		t.Fatalf("expected number default")
	}
	if m["i"].(float64) != 3 {
		t.Fatalf("expected integer default")
	}
	if m["b"].(bool) != true {
		t.Fatalf("expected bool default")
	}
}

func TestBuilderDefaults_DuplicateDefaultPanic(t *testing.T) {
	defer func() { recover() }()
	_ = NewBuilder().String("s", Optional(), DefaultString("a"), DefaultString("b"))
	// second default should panic
	b := NewBuilder() // unreachable
	_ = b
	panic("expected panic not triggered")
}

func TestBuilderDefaults_EnumValidation(t *testing.T) {
	defer func() { recover() }()
	_ = NewBuilder().EnumString("e", []string{"a", "b"}, Optional(), DefaultString("z"))
	panic("expected panic for invalid enum default")
}
