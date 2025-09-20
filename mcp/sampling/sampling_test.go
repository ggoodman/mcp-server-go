package sampling

import (
	"testing"

	"github.com/ggoodman/mcp-server-go/mcp"
)

func TestNewCreateMessageBasic(t *testing.T) {
	msg := UserText("hello")
	r := NewCreateMessage([]mcp.SamplingMessage{msg}, WithSystemPrompt("system"), WithMaxTokens(10))
	if err := ValidateCreateMessage(r); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := r.SystemPrompt; got != "system" {
		t.Fatalf("systemPrompt mismatch: %s", got)
	}
	if r.MaxTokens != 10 {
		t.Fatalf("maxTokens mismatch: %d", r.MaxTokens)
	}
	if len(r.Messages) != 1 || r.Messages[0].Content.Text != "hello" {
		t.Fatalf("unexpected messages: %#v", r.Messages)
	}
}

func TestValidateCreateMessageErrors(t *testing.T) {
	if err := ValidateCreateMessage(nil); err == nil {
		t.Fatal("expected error for nil request")
	}
	if err := ValidateCreateMessage(&mcp.CreateMessageRequest{}); err == nil {
		t.Fatal("expected error for empty messages")
	}
}
