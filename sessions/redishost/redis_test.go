package redishost

import (
	"testing"

	"github.com/ggoodman/mcp-streaming-http-go/sessions"
	"github.com/ggoodman/mcp-streaming-http-go/sessions/sessionhosttest"
)

func TestRedisSessionHost(t *testing.T) {
	// Quick availability check to allow graceful skip in environments without Redis
	h, err := NewFromEnv()
	if err != nil {
		t.Skipf("skipping redis session host tests: %v", err)
		return
	}
	_ = h.Close()

	sessionhosttest.RunSessionHostTests(t, func(t *testing.T) sessions.SessionHost {
		hh, err := NewFromEnv()
		if err != nil {
			t.Fatalf("NewFromEnv: %v", err)
		}
		return hh
	})
}
