package memory

import (
	"context"
	"testing"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/storage"
)

func TestNew(t *testing.T) {
	s, err := New(100)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer s.Close()

	if s == nil {
		t.Fatal("New() returned nil storage")
	}
}

func TestGlobalStorage(t *testing.T) {
	s, err := New(100)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	key := "test-key"
	data := []byte("test-data")

	// Set global data
	err = s.Set(ctx, key, data)
	if err != nil {
		t.Fatalf("Set() failed: %v", err)
	}

	// Get global data
	item, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	if item == nil {
		t.Fatal("Get() returned nil item")
	}

	if string(item.Data) != string(data) {
		t.Fatalf("Get() returned wrong data: got %s, want %s", string(item.Data), string(data))
	}
}

func TestUserStorage(t *testing.T) {
	s, err := New(100)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	userID := "user123"
	key := "preferences"
	data := []byte(`{"theme": "dark"}`)

	// Set user data
	err = s.Set(ctx, key, data, storage.WithUser(userID))
	if err != nil {
		t.Fatalf("Set() failed: %v", err)
	}

	// Get user data
	item, err := s.Get(ctx, key, storage.WithUser(userID))
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	if item == nil {
		t.Fatal("Get() returned nil item")
	}

	if string(item.Data) != string(data) {
		t.Fatalf("Get() returned wrong data: got %s, want %s", string(item.Data), string(data))
	}
}

func TestSessionStorage(t *testing.T) {
	s, err := New(100)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	userID := "user123"
	sessionID := "session456"
	key := "state"
	data := []byte(`{"last_action": "connect"}`)

	// Set session data
	err = s.Set(ctx, key, data, storage.WithUserSession(userID, sessionID))
	if err != nil {
		t.Fatalf("Set() failed: %v", err)
	}

	// Get session data
	item, err := s.Get(ctx, key, storage.WithUserSession(userID, sessionID))
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	if item == nil {
		t.Fatal("Get() returned nil item")
	}

	if string(item.Data) != string(data) {
		t.Fatalf("Get() returned wrong data: got %s, want %s", string(item.Data), string(data))
	}
}

func TestNamespaceIsolation(t *testing.T) {
	s, err := New(100)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	key := "test-key"

	globalData := []byte("global-data")
	userData := []byte("user-data")
	sessionData := []byte("session-data")

	// Set data in different namespaces
	err = s.Set(ctx, key, globalData)
	if err != nil {
		t.Fatalf("Set() global failed: %v", err)
	}

	err = s.Set(ctx, key, userData, storage.WithUser("user123"))
	if err != nil {
		t.Fatalf("Set() user failed: %v", err)
	}

	err = s.Set(ctx, key, sessionData, storage.WithUserSession("user123", "session456"))
	if err != nil {
		t.Fatalf("Set() session failed: %v", err)
	}

	// Verify isolation
	item, err := s.Get(ctx, key)
	if err != nil || item == nil || string(item.Data) != string(globalData) {
		t.Fatal("Global data not isolated")
	}

	item, err = s.Get(ctx, key, storage.WithUser("user123"))
	if err != nil || item == nil || string(item.Data) != string(userData) {
		t.Fatal("User data not isolated")
	}

	item, err = s.Get(ctx, key, storage.WithUserSession("user123", "session456"))
	if err != nil || item == nil || string(item.Data) != string(sessionData) {
		t.Fatal("Session data not isolated")
	}
}

func TestTTL(t *testing.T) {
	s, err := New(100)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	key := "ttl-key"
	data := []byte("ttl-data")
	ttl := 100 * time.Millisecond

	// Set data with TTL
	err = s.Set(ctx, key, data, storage.WithTTL(ttl))
	if err != nil {
		t.Fatalf("Set() with TTL failed: %v", err)
	}

	// Get data immediately - should exist
	item, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	if item == nil {
		t.Fatal("Get() returned nil item before expiration")
	}

	// Wait for expiration
	time.Sleep(ttl + 50*time.Millisecond)

	// Get data after expiration - should return nil
	item, err = s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() failed after expiration: %v", err)
	}

	if item != nil {
		t.Fatal("Get() returned non-nil item after expiration")
	}
}

func TestDeleteKey(t *testing.T) {
	s, err := New(100)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	userID := "user123"
	key := "test-key"
	data := []byte("test-data")

	// Set data
	err = s.Set(ctx, key, data, storage.WithUser(userID))
	if err != nil {
		t.Fatalf("Set() failed: %v", err)
	}

	// Verify data exists
	item, err := s.Get(ctx, key, storage.WithUser(userID))
	if err != nil || item == nil {
		t.Fatal("Data should exist before deletion")
	}

	// Delete specific key
	err = s.Delete(ctx, storage.WithUser(userID), storage.WithKey(key))
	if err != nil {
		t.Fatalf("Delete() failed: %v", err)
	}

	// Verify data is gone
	item, err = s.Get(ctx, key, storage.WithUser(userID))
	if err != nil {
		t.Fatalf("Get() failed after deletion: %v", err)
	}

	if item != nil {
		t.Fatal("Data should not exist after deletion")
	}
}

func TestDeleteNamespace(t *testing.T) {
	s, err := New(100)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	userID := "user123"
	sessionID := "session456"

	// Set multiple keys in session
	keys := []string{"key1", "key2", "key3"}
	for _, key := range keys {
		data := []byte("data-" + key)
		err = s.Set(ctx, key, data, storage.WithUserSession(userID, sessionID))
		if err != nil {
			t.Fatalf("Set() failed for %s: %v", key, err)
		}
	}

	// Verify all keys exist
	for _, key := range keys {
		item, err := s.Get(ctx, key, storage.WithUserSession(userID, sessionID))
		if err != nil || item == nil {
			t.Fatalf("Key %s should exist before deletion", key)
		}
	}

	// Delete entire session namespace
	err = s.Delete(ctx, storage.WithUserSession(userID, sessionID))
	if err != nil {
		t.Fatalf("Delete() namespace failed: %v", err)
	}

	// Verify all keys are gone
	for _, key := range keys {
		item, err := s.Get(ctx, key, storage.WithUserSession(userID, sessionID))
		if err != nil {
			t.Fatalf("Get() failed after namespace deletion: %v", err)
		}

		if item != nil {
			t.Fatalf("Key %s should not exist after namespace deletion", key)
		}
	}
}

func TestNotFound(t *testing.T) {
	s, err := New(100)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// Get non-existent key
	item, err := s.Get(ctx, "non-existent-key")
	if err != nil {
		t.Fatalf("Get() should not return error for non-existent key: %v", err)
	}

	if item != nil {
		t.Fatal("Get() should return nil for non-existent key")
	}
}
