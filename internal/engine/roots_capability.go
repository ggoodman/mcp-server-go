package engine

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// Ensure interface compliance
var _ sessions.RootsCapability = (*rootsCapability)(nil)

type rootsCapability struct {
	eng                 *Engine
	log                 *slog.Logger
	sessID              string
	userID              string
	requestScopedWriter MessageWriter
	sessionScopedWriter MessageWriter
}

func (r *rootsCapability) ListRoots(ctx context.Context) (*mcp.ListRootsResult, error) {
	reqID, err := newClientMessageID()
	if err != nil {
		r.log.Error("roots.list.err", slog.String("session_id", r.sessID), slog.String("user_id", r.userID), slog.String("err", err.Error()))
		return nil, ErrInternal
	}

	clientReq := jsonrpc.Request{
		JSONRPCVersion: jsonrpc.ProtocolVersion,
		Method:         string(mcp.RootsListMethod),
		ID:             jsonrpc.NewRequestID(reqID),
		// no params
	}
	bytes, err := json.Marshal(clientReq)
	if err != nil {
		r.log.Error("roots.list.marshal.fail", slog.String("session_id", r.sessID), slog.String("user_id", r.userID), slog.String("err", err.Error()))
		return nil, ErrInternal
	}

	rdvCh, close := r.eng.createRendezVous(reqID)
	defer close()

	if err := r.requestScopedWriter.WriteMessage(ctx, bytes); err != nil {
		r.log.Error("roots.list.write.fail", slog.String("session_id", r.sessID), slog.String("user_id", r.userID), slog.String("err", err.Error()))
		return nil, ErrInternal
	}

	select {
	case msg, ok := <-rdvCh:
		if !ok {
			r.log.Error("roots.list.err", slog.String("session_id", r.sessID), slog.String("user_id", r.userID), slog.String("err", "rendez-vous closed"))
			return nil, ErrCancelled
		}
		var resp jsonrpc.Response
		if err := json.Unmarshal(msg, &resp); err != nil {
			r.log.Error("roots.list.unmarshal.fail", slog.String("session_id", r.sessID), slog.String("user_id", r.userID), slog.String("err", err.Error()))
			return nil, ErrInternal
		}
		if resp.Error != nil {
			r.log.Error("roots.list.error", slog.String("session_id", r.sessID), slog.String("user_id", r.userID), slog.Int("code", int(resp.Error.Code)), slog.String("message", resp.Error.Message))
			return nil, ErrInternal
		}
		var res mcp.ListRootsResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			r.log.Error("roots.list.result.unmarshal.fail", slog.String("session_id", r.sessID), slog.String("user_id", r.userID), slog.String("err", err.Error()))
			return nil, ErrInternal
		}
		return &res, nil
	case <-ctx.Done():
		r.log.Error("roots.list.err", slog.String("session_id", r.sessID), slog.String("user_id", r.userID), slog.String("err", "context done before response"))
		return nil, ctx.Err()
	}
}

func (r *rootsCapability) RegisterRootsListChangedListener(ctx context.Context, listener sessions.RootsListChangedListener) (bool, error) {
	// Not yet wired to any real change source; advertise unsupported for now.
	return false, nil
}
