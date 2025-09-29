package redishost

import (
	"fmt"
	"testing"
	"time"

	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/sessionhosttest"
)

func TestRedisSessionHost(t *testing.T) {
	// Quick availability check to allow graceful skip in environments without Redis
	h, err := New("redis://localhost:6379")
	if err != nil {
		t.Skipf("skipping redis session host tests: %v", err)
		return
	}
	_ = h.Close()

	sessionhosttest.RunSessionHostTests(t, func(t *testing.T) sessions.SessionHost {
		// Unique prefix per test run to avoid interference across runs
		prefix := fmt.Sprintf("mcp:sessions:test:%d:", time.Now().UnixNano())
		hh, err := New("redis://localhost:6379", WithKeyPrefix(prefix))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return hh
	})
}
