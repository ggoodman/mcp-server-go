package elicitation

import (
	"testing"
)

func TestObjectSchemaBasic(t *testing.T) {
	s := ObjectSchema(
		PropString("language", "Target language", WithEnum("French", "Spanish")),
		Required("language"),
	)
	if err := ValidateObjectSchema(&s); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if s.Type != "object" {
		t.Fatalf("type mismatch: %s", s.Type)
	}
	if _, ok := s.Properties["language"]; !ok {
		t.Fatal("missing language property")
	}
	if len(s.Required) != 1 || s.Required[0] != "language" {
		t.Fatalf("required mismatch: %#v", s.Required)
	}
}

func TestValidateObjectSchemaErrors(t *testing.T) {
	empty := ObjectSchema()
	if err := ValidateObjectSchema(&empty); err == nil {
		t.Fatal("expected error for empty properties")
	}

	badReq := ObjectSchema(PropString("language", "desc"), Required("missing"))
	if err := ValidateObjectSchema(&badReq); err == nil {
		t.Fatal("expected error for missing required property")
	}
}

func TestValidateObjectSchemaDedupRequired(t *testing.T) {
	s := ObjectSchema(PropString("a", "A"), Required("a", "a", "a"))
	if err := ValidateObjectSchema(&s); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(s.Required) != 1 {
		t.Fatalf("expected deduped required length 1, got %d", len(s.Required))
	}
}
