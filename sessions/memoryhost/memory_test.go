package memoryhost

import (
	"context"
	"testing"

	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/sessionhosttest"
)

func TestMemorySessionHost(t *testing.T) {
	sessionhosttest.RunSessionHostTests(t, func(t *testing.T) sessions.SessionHost {
		return New()
	})
}

func TestCreateSessionRejectsDuplicateID(t *testing.T) {
	h := New()
	ctx := context.Background()

	sid := "dup-id"
	// First creation should succeed
	if err := h.CreateSession(ctx, &sessions.SessionMetadata{SessionID: sid, UserID: "user-1"}); err != nil {
		t.Fatalf("first CreateSession failed: %v", err)
	}

	// Second creation with same ID must fail and not clobber existing record
	if err := h.CreateSession(ctx, &sessions.SessionMetadata{SessionID: sid, UserID: "user-2"}); err == nil {
		t.Fatalf("expected CreateSession to fail for duplicate SessionID, got nil error")
	}

	got, err := h.GetSession(ctx, sid)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got.UserID != "user-1" {
		t.Fatalf("session metadata was clobbered; want user-1, got %s", got.UserID)
	}
}
