package memoryhost

import (
	"testing"

	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/sessionhosttest"
)

func TestMemorySessionHost(t *testing.T) {
	sessionhosttest.RunSessionHostTests(t, func(t *testing.T) sessions.SessionHost {
		return New()
	})
}
