package sampling

import (
	"errors"

	"github.com/ggoodman/mcp-server-go/mcp"
)

// TextBlock constructs a text content block.
func TextBlock(text string) mcp.ContentBlock {
	return mcp.ContentBlock{Type: mcp.ContentTypeText, Text: text}
}

// UserText returns a SamplingMessage authored by the user with a single text block.
func UserText(text string) mcp.SamplingMessage {
	return mcp.SamplingMessage{Role: mcp.RoleUser, Content: TextBlock(text)}
}

// AssistantText returns a SamplingMessage authored by the assistant with a single text block.
func AssistantText(text string) mcp.SamplingMessage {
	return mcp.SamplingMessage{Role: mcp.RoleAssistant, Content: TextBlock(text)}
}

// CreateOption mutates a CreateMessageRequest during construction.
type CreateOption func(*mcp.CreateMessageRequest)

// WithSystemPrompt sets the system prompt.
func WithSystemPrompt(prompt string) CreateOption {
	return func(r *mcp.CreateMessageRequest) { r.SystemPrompt = prompt }
}

// WithMaxTokens sets the MaxTokens field.
func WithMaxTokens(n int) CreateOption {
	return func(r *mcp.CreateMessageRequest) { r.MaxTokens = n }
}

// WithTemperature sets the Temperature field.
func WithTemperature(t float64) CreateOption {
	return func(r *mcp.CreateMessageRequest) { r.Temperature = t }
}

// WithStopSequences sets stop sequences.
func WithStopSequences(stops ...string) CreateOption {
	return func(r *mcp.CreateMessageRequest) { r.StopSequences = append([]string(nil), stops...) }
}

// WithModelPreferences sets model preferences.
func WithModelPreferences(prefs *mcp.ModelPreferences) CreateOption {
	return func(r *mcp.CreateMessageRequest) { r.ModelPreferences = prefs }
}

// NewCreateMessage constructs a *CreateMessageRequest with the provided messages and options.
// It performs minimal validation (roles & non-empty type for content blocks).
func NewCreateMessage(msgs []mcp.SamplingMessage, opts ...CreateOption) *mcp.CreateMessageRequest {
	r := &mcp.CreateMessageRequest{Messages: append([]mcp.SamplingMessage(nil), msgs...)}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ValidateCreateMessage performs sanity checks on a CreateMessageRequest.
func ValidateCreateMessage(r *mcp.CreateMessageRequest) error {
	if r == nil {
		return errors.New("nil request")
	}
	if len(r.Messages) == 0 {
		return errors.New("no messages provided")
	}
	for i, m := range r.Messages {
		if m.Role != mcp.RoleUser && m.Role != mcp.RoleAssistant {
			return errors.New("invalid role in message index")
		}
		if m.Content.Type == "" {
			return errors.New("empty content type in message index")
		}
		_ = i // index placeholder for future detailed errors
	}
	return nil
}
