package redishost

import (
	"testing"

	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/sessionhosttest"
)

func TestRedisSessionHost(t *testing.T) {
	// Quick availability check to allow graceful skip in environments without Redis
	h, err := New("localhost:6379")
	if err != nil {
		t.Skipf("skipping redis session host tests: %v", err)
		return
	}
	_ = h.Close()

	sessionhosttest.RunSessionHostTests(t, func(t *testing.T) sessions.SessionHost {
		hh, err := New("localhost:6379", WithKeyPrefix("mcp:sessions:test:"))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return hh
	})
}
