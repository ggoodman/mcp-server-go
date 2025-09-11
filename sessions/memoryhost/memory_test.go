package memoryhost

import (
	"testing"

	"github.com/ggoodman/mcp-streaming-http-go/sessions"
	"github.com/ggoodman/mcp-streaming-http-go/sessions/sessionhosttest"
)

func TestMemorySessionHost(t *testing.T) {
	sessionhosttest.RunSessionHostTests(t, func(t *testing.T) sessions.SessionHost {
		return New()
	})
}
