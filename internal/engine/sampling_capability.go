package engine

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

var _ sessions.SamplingCapability = (*samplingCapabilty)(nil)

type samplingCapabilty struct {
	eng                 *Engine
	log                 *slog.Logger
	sessID              string
	userID              string
	requestScopedWriter MessageWriter
	sessionScopedWriter MessageWriter
}

// CreateMessage implements sessions.SamplingCapability.
func (s *samplingCapabilty) CreateMessage(ctx context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	reqID, err := newClientMessageID()
	if err != nil {
		s.log.Error("sampling.create_message.err", slog.String("session_id", s.sessID), slog.String("user_id", s.userID), slog.String("err", err.Error()))
		return nil, ErrInternal
	}

	params, err := json.Marshal(req)
	if err != nil {
		s.log.Error("sampling.create_message.err", slog.String("session_id", s.sessID), slog.String("user_id", s.userID), slog.String("err", err.Error()))
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
		s.log.Error("failed to marshal sampling create message request", slog.String("session_id", s.sessID), slog.String("user_id", s.userID), slog.String("err", err.Error()))
		return nil, ErrInternal
	}

	// We need to create a rendez-vous so that we can receive the response to this request.
	rdvCh, close := s.eng.createRendezVous(reqID)
	defer close()

	if err := s.requestScopedWriter.WriteMessage(ctx, bytes); err != nil {
		s.log.Error("failed to write sampling create message request", slog.String("session_id", s.sessID), slog.String("user_id", s.userID), slog.String("err", err.Error()))
		return nil, ErrInternal
	}

	// Wait for our response or context cancellation.
	select {
	case msg, ok := <-rdvCh:
		if !ok {
			// A notifications/cancelled message cancelled this
			s.log.Error("sampling.create_message.err", slog.String("session_id", s.sessID), slog.String("user_id", s.userID), slog.String("err", "rendez-vous channel closed"))
			return nil, ErrCancelled
		}

		var res mcp.CreateMessageResult
		if err := json.Unmarshal(msg, &res); err != nil {
			s.log.Error("sampling.create_message.err", slog.String("session_id", s.sessID), slog.String("user_id", s.userID), slog.String("err", err.Error()))
			return nil, ErrInternal
		}

		return &res, nil
	case <-ctx.Done():
		s.log.Error("sampling.create_message.err", slog.String("session_id", s.sessID), slog.String("user_id", s.userID), slog.String("err", "context done before response received"))
		return nil, ctx.Err()
	}
}
