package mcpservice

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// --- Logging capability helpers ---

// NewSlogLevelVarLogging returns a LoggingCapability that maps MCP LoggingLevel
// to a provided slog.LevelVar. This adjusts process-wide slog level when used
// with handlers created from the same LevelVar.
func NewSlogLevelVarLogging(lv *slog.LevelVar) LoggingCapability {
	return &slogLevelVarLogging{lv: lv}
}

type slogLevelVarLogging struct{ lv *slog.LevelVar }

// ProvideLogging implements LoggingCapabilityProvider for static slog level var logging.
func (l *slogLevelVarLogging) ProvideLogging(ctx context.Context, session sessions.Session) (LoggingCapability, bool, error) {
	if l == nil {
		return nil, false, nil
	}
	return l, true, nil
}

func (l *slogLevelVarLogging) SetLevel(ctx context.Context, _ sessions.Session, level mcp.LoggingLevel) error {
	if l == nil || l.lv == nil {
		return nil
	}
	if !mcp.IsValidLoggingLevel(level) {
		return ErrInvalidLoggingLevel
	}
	var slogLevel slog.Level
	switch level {
	case mcp.LoggingLevelDebug:
		slogLevel = slog.LevelDebug
	case mcp.LoggingLevelInfo, mcp.LoggingLevelNotice:
		// Map notice to info
		slogLevel = slog.LevelInfo
	case mcp.LoggingLevelWarning:
		slogLevel = slog.LevelWarn
	case mcp.LoggingLevelError, mcp.LoggingLevelCritical, mcp.LoggingLevelAlert, mcp.LoggingLevelEmergency:
		// Map error and above to error
		slogLevel = slog.LevelError
	default:
		// Unknown -> leave unchanged
		return ErrInvalidLoggingLevel
	}
	l.lv.Set(slogLevel)
	return nil
}

// ErrInvalidLoggingLevel indicates the provided level is not one of the
// protocol-defined LoggingLevel values.
var ErrInvalidLoggingLevel = errors.New("invalid logging level")
