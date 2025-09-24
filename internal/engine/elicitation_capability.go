package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"

	"github.com/ggoodman/mcp-server-go/internal/elicitation"
	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

var _ sessions.ElicitationCapability = (*elicitationCapability)(nil)

type elicitationCapability struct {
	eng                 *Engine
	log                 *slog.Logger
	sessID              string
	userID              string
	requestScopedWriter MessageWriter
	sessionScopedWriter MessageWriter
}

// Elicit implements sessions.ElicitationCapability.
func (c *elicitationCapability) Elicit(ctx context.Context, text string, target any, opts ...sessions.ElicitOption) (sessions.ElicitAction, error) {
	// Derive schema from target type and retain cached projection for decoding
	t := reflect.TypeOf(target)
	schema, cp, err := elicitation.ProjectSchema(t)
	if err != nil {
		c.log.Error("elicitation.project.err", slog.String("session_id", c.sessID), slog.String("user_id", c.userID), slog.String("err", err.Error()))
		return sessions.ElicitActionCancel, ErrInternal
	}

	// Accumulate options
	var cfg sessions.ElicitConfig
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}

	// Build client request
	reqID, err := newClientMessageID()
	if err != nil {
		c.log.Error("elicitation.create.err", slog.String("session_id", c.sessID), slog.String("user_id", c.userID), slog.String("err", err.Error()))
		return sessions.ElicitActionCancel, ErrInternal
	}

	params, err := json.Marshal(mcp.ElicitRequest{Message: text, RequestedSchema: *schema})
	if err != nil {
		c.log.Error("elicitation.create.err", slog.String("session_id", c.sessID), slog.String("user_id", c.userID), slog.String("err", err.Error()))
		return sessions.ElicitActionCancel, ErrInternal
	}

	clientReq := jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ElicitationCreateMethod), Params: json.RawMessage(params), ID: jsonrpc.NewRequestID(reqID)}

	bytes, err := json.Marshal(clientReq)
	if err != nil {
		c.log.Error("elicitation.create.marshal.err", slog.String("session_id", c.sessID), slog.String("user_id", c.userID), slog.String("err", err.Error()))
		return sessions.ElicitActionCancel, ErrInternal
	}

	// Create rendezvous for the response
	rdvCh, closeRdv := c.eng.createRendezVous(reqID)
	defer closeRdv()

	if err := c.requestScopedWriter.WriteMessage(ctx, bytes); err != nil {
		c.log.Error("elicitation.create.write.fail", slog.String("session_id", c.sessID), slog.String("user_id", c.userID), slog.String("err", err.Error()))
		return sessions.ElicitActionCancel, ErrInternal
	}

	// Wait for response or cancellation
	select {
	case msg, ok := <-rdvCh:
		if !ok {
			c.log.Error("elicitation.create.cancelled", slog.String("session_id", c.sessID), slog.String("user_id", c.userID))
			return sessions.ElicitActionCancel, ErrCancelled
		}

		// Decode JSON-RPC response envelope first
		var resp jsonrpc.Response
		if err := json.Unmarshal(msg, &resp); err != nil {
			c.log.Error("elicitation.create.unmarshal_response.fail", slog.String("session_id", c.sessID), slog.String("user_id", c.userID), slog.String("err", err.Error()))
			return sessions.ElicitActionCancel, ErrInternal
		}
		if resp.Error != nil {
			c.log.Error("elicitation.create.error", slog.String("session_id", c.sessID), slog.String("user_id", c.userID), slog.Int("code", int(resp.Error.Code)), slog.String("message", resp.Error.Message))
			return sessions.ElicitActionCancel, ErrInternal
		}

		// Unmarshal result payload
		var er mcp.ElicitResult
		if err := json.Unmarshal(resp.Result, &er); err != nil {
			c.log.Error("elicitation.create.result.unmarshal.fail", slog.String("session_id", c.sessID), slog.String("user_id", c.userID), slog.String("err", err.Error()))
			return sessions.ElicitActionCancel, ErrInternal
		}

		// Map action string to typed action
		var action sessions.ElicitAction
		switch er.Action {
		case string(sessions.ElicitActionAccept):
			action = sessions.ElicitActionAccept
		case string(sessions.ElicitActionDecline):
			action = sessions.ElicitActionDecline
		case string(sessions.ElicitActionCancel):
			action = sessions.ElicitActionCancel
		default:
			action = sessions.ElicitActionCancel
		}

		// Optionally capture raw content
		if cfg.RawDst != nil {
			// shallow copy
			m := make(map[string]any, len(er.Content))
			for k, v := range er.Content {
				m[k] = v
			}
			*cfg.RawDst = m
		}

		// Only decode into target on accept
		if action == sessions.ElicitActionAccept {
			if err := elicitation.DecodeForTyped(cp, target, er.Content, cfg.Strict); err != nil {
				c.log.Error("elicitation.create.decode.fail", slog.String("session_id", c.sessID), slog.String("user_id", c.userID), slog.String("err", err.Error()))
				return sessions.ElicitActionCancel, ErrInternal
			}
		}

		return action, nil
	case <-ctx.Done():
		c.log.Error("elicitation.create.ctx.done", slog.String("session_id", c.sessID), slog.String("user_id", c.userID))
		return sessions.ElicitActionCancel, ctx.Err()
	}
}
