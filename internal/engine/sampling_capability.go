package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/ggoodman/mcp-server-go/internal/createmessage"
	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/sampling"
	"github.com/google/uuid"
)

var _ sessions.SamplingCapability = (*samplingCapabilty)(nil)

type samplingCapabilty struct {
	eng                 *Engine
	log                 *slog.Logger
	sessID              string
	userID              string
	requestScopedWriter MessageWriter
}

// CreateMessage implements sessions.SamplingCapability.
func (s *samplingCapabilty) CreateMessage(ctx context.Context, system string, user sampling.Message, opts ...sampling.Option) (*sessions.SampleResult, error) {
	spec := sampling.CreateSpec{}.Compile(opts...)
	cfg := &createmessage.Config{System: system, User: user}
	if spec.ModelPrefs != nil {
		cfg.ModelPrefs = spec.ModelPrefs
	}
	if spec.Temperature != nil {
		cfg.Temperature = *spec.Temperature
	}
	if spec.MaxTokens != nil {
		cfg.MaxTokens = *spec.MaxTokens
	}
	if len(spec.History) > 0 {
		cfg.History = spec.History
	}
	if len(spec.StopSequences) > 0 {
		cfg.StopSequences = spec.StopSequences
	}
	if spec.Metadata != nil {
		cfg.Metadata = spec.Metadata
	}
	if spec.IncludeContext != "" {
		cfg.IncludeContext = spec.IncludeContext
	}
	req, err := createmessage.BuildCreateMessageRequest(cfg)
	if err != nil {
		return nil, err
	}

	if user.Role != sampling.RoleUser {
		return nil, errors.New("sampling: user message role must be 'user'")
	}

	reqID := uuid.NewString()
	params, err := json.Marshal(req)
	if err != nil {
		s.log.ErrorContext(ctx, "sampling.create_message.err", slog.String("err", err.Error()))
		return nil, ErrInternal
	}

	clientReq := jsonrpc.Request{
		JSONRPCVersion: "2.0",
		Method:         string(mcp.SamplingCreateMessageMethod),
		Params:         json.RawMessage(params),
		ID:             jsonrpc.NewRequestID(reqID),
	}

	bytes, err := json.Marshal(clientReq)
	if err != nil {
		s.log.ErrorContext(ctx, "failed to marshal sampling create message request", slog.String("err", err.Error()))
		return nil, ErrInternal
	}

	// We need to create a rendez-vous so that we can receive the response to this request.
	rdvCh, close := s.eng.createRendezVous(reqID)
	defer close()

	if err := s.requestScopedWriter.WriteMessage(ctx, bytes); err != nil {
		s.log.ErrorContext(ctx, "failed to write sampling create message request", slog.String("err", err.Error()))
		return nil, ErrInternal
	}

	// Wait for our response or context cancellation.
	select {
	case msg, ok := <-rdvCh:
		if !ok {
			// A notifications/cancelled message cancelled this
			s.log.ErrorContext(ctx, "sampling.create_message.err", slog.String("err", "rendez-vous channel closed"))
			return nil, ErrCancelled
		}

		// Decode JSON-RPC response envelope first, then extract result payload
		var resp jsonrpc.Response
		if err := json.Unmarshal(msg, &resp); err != nil {
			s.log.ErrorContext(ctx, "sampling.create_message.unmarshal_response.fail", slog.String("err", err.Error()))
			return nil, ErrInternal
		}
		if resp.Error != nil {
			s.log.ErrorContext(ctx, "sampling.create_message.error", slog.Int("code", int(resp.Error.Code)), slog.String("message", resp.Error.Message))
			return nil, ErrInternal
		}

		var res mcp.CreateMessageResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			s.log.ErrorContext(ctx, "sampling.create_message.result.unmarshal.fail", slog.String("err", err.Error()))
			return nil, ErrInternal
		}

		// Inline mapping (was sessions.FromProtocolResult previously).
		sampledMsg := sampling.Message{Role: sampling.Role(res.Role)}
		switch res.Content.Type {
		case mcp.ContentTypeText:
			sampledMsg.Content = sampling.Text{Text: res.Content.Text}
		case mcp.ContentTypeImage:
			data, _ := base64.StdEncoding.DecodeString(res.Content.Data)
			sampledMsg.Content = sampling.Image{MIMEType: res.Content.MimeType, Data: data}
		case mcp.ContentTypeAudio:
			data, _ := base64.StdEncoding.DecodeString(res.Content.Data)
			sampledMsg.Content = sampling.Audio{MIMEType: res.Content.MimeType, Data: data}
		default:
			sampledMsg.Content = sampling.Text{Text: ""}
		}
		var meta map[string]any
		if res.Meta != nil {
			cp := make(map[string]any, len(res.Meta))
			for k, v := range res.Meta {
				cp[k] = v
			}
			meta = cp
		}
		return &sessions.SampleResult{Message: sampledMsg, Model: res.Model, StopReason: res.StopReason, Meta: meta}, nil
	case <-ctx.Done():
		s.log.ErrorContext(ctx, "sampling.create_message.err", slog.String("err", "context done before response received"))
		return nil, ctx.Err()
	}
}

// (legacy helper code removed; conversion handled in sessions package)
