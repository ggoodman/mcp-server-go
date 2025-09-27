package createmessage

import (
	"encoding/base64"
	"errors"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions/sampling"
)

// Config accumulates sampling request parameters. Internal to engine + sessions.
// All fields are optional unless otherwise stated.
type Config struct {
	System         string
	User           sampling.Message // required
	History        []sampling.Message
	ModelPrefs     *mcp.ModelPreferences
	Temperature    float64
	MaxTokens      int
	StopSequences  []string
	Metadata       map[string]any
	IncludeContext string
}

// BuildCreateMessageRequest validates and converts the config into a protocol request.
func BuildCreateMessageRequest(cfg *Config) (*mcp.CreateMessageRequest, error) {
	if cfg == nil {
		return nil, errors.New("internal/sampling: nil config")
	}
	if cfg.User.Role != sampling.RoleUser {
		return nil, errors.New("internal/sampling: user message must have role 'user'")
	}
	// detect duplicate system
	for _, h := range cfg.History {
		if h.Role == sampling.RoleSystem && cfg.System != "" {
			return nil, errors.New("internal/sampling: duplicate system prompt")
		}
	}

	wireMsgs := make([]mcp.SamplingMessage, 0, len(cfg.History)+1)
	for _, h := range cfg.History {
		if h.Role == sampling.RoleSystem {
			continue
		}
		wm, err := toWire(h)
		if err != nil {
			return nil, err
		}
		wireMsgs = append(wireMsgs, wm)
	}
	uw, err := toWire(cfg.User)
	if err != nil {
		return nil, err
	}
	wireMsgs = append(wireMsgs, uw)

	req := &mcp.CreateMessageRequest{
		Messages:         wireMsgs,
		SystemPrompt:     cfg.System,
		ModelPreferences: cfg.ModelPrefs,
		IncludeContext:   cfg.IncludeContext,
		Temperature:      cfg.Temperature,
		MaxTokens:        cfg.MaxTokens,
		StopSequences:    append([]string(nil), cfg.StopSequences...),
		Metadata:         cfg.Metadata,
	}
	return req, nil
}

// Map protocol result to ergonomic message + meta fields.
func MapResult(res *mcp.CreateMessageResult) (sampling.Message, map[string]any) {
	if res == nil {
		return sampling.Message{}, nil
	}
	msg := sampling.Message{Role: sampling.Role(res.Role)}
	switch res.Content.Type {
	case mcp.ContentTypeText:
		msg.Content = sampling.Text{Text: res.Content.Text}
	case mcp.ContentTypeImage:
		data, _ := base64.StdEncoding.DecodeString(res.Content.Data)
		msg.Content = sampling.Image{MIMEType: res.Content.MimeType, Data: data}
	case mcp.ContentTypeAudio:
		data, _ := base64.StdEncoding.DecodeString(res.Content.Data)
		msg.Content = sampling.Audio{MIMEType: res.Content.MimeType, Data: data}
	default:
		msg.Content = sampling.Text{Text: ""}
	}
	var meta map[string]any
	if res.Meta != nil {
		cp := make(map[string]any, len(res.Meta))
		for k, v := range res.Meta {
			cp[k] = v
		}
		meta = cp
	}
	return msg, meta
}

func toWire(m sampling.Message) (mcp.SamplingMessage, error) {
	switch c := m.Content.(type) {
	case sampling.Text:
		return mcp.SamplingMessage{Role: mcp.Role(m.Role), Content: mcp.ContentBlock{Type: mcp.ContentTypeText, Text: c.Text}}, nil
	case sampling.Image:
		enc := base64.StdEncoding.EncodeToString(c.Data)
		return mcp.SamplingMessage{Role: mcp.Role(m.Role), Content: mcp.ContentBlock{Type: mcp.ContentTypeImage, Data: enc, MimeType: c.MIMEType}}, nil
	case sampling.Audio:
		enc := base64.StdEncoding.EncodeToString(c.Data)
		return mcp.SamplingMessage{Role: mcp.Role(m.Role), Content: mcp.ContentBlock{Type: mcp.ContentTypeAudio, Data: enc, MimeType: c.MIMEType}}, nil
	default:
		return mcp.SamplingMessage{}, errors.New("internal/sampling: unsupported content type")
	}
}
