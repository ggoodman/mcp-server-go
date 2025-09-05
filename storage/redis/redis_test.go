package redis

import (
	"context"
	"testing"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/storage"
	"github.com/redis/go-redis/v9"
)

func TestRedisStorage(t *testing.T) {
	// Skip test if Redis is not available
	client := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:6379",
		DB:   2, // Use separate DB for storage tests
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	// Clean up test data
	defer client.FlushDB(ctx)

	s, err := New(Config{
		Client: client,
	})
	if err != nil {
		t.Fatalf("Failed to create Redis storage: %v", err)
	}
	defer s.Close()

	t.Run("SetAndGet", func(t *testing.T) {
		testSetAndGet(t, s)
	})

	t.Run("GetNonExistent", func(t *testing.T) {
		testGetNonExistent(t, s)
	})

	t.Run("TTL", func(t *testing.T) {
		testTTL(t, s)
	})

	t.Run("Namespaces", func(t *testing.T) {
		testNamespaces(t, s)
	})

	t.Run("DeleteKey", func(t *testing.T) {
		testDeleteKey(t, s)
	})

	t.Run("DeleteNamespace", func(t *testing.T) {
		testDeleteNamespace(t, s)
	})
}

func testSetAndGet(t *testing.T, s storage.Storage) {
	ctx := context.Background()
	key := "test-key"
	data := []byte("test data")

	// Set data
	err := s.Set(ctx, key, data)
	if err != nil {
		t.Fatalf("Failed to set data: %v", err)
	}

	// Get data
	item, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}

	if item == nil {
		t.Fatal("Expected item to exist, got nil")
	}

	if string(item.Data) != string(data) {
		t.Errorf("Expected data %s, got %s", data, item.Data)
	}

	if item.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}

	if item.ExpiresAt != nil {
		t.Error("ExpiresAt should be nil for data without TTL")
	}
}

func testGetNonExistent(t *testing.T, s storage.Storage) {
	ctx := context.Background()
	key := "non-existent-key"

	// Get non-existent key
	item, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get non-existent key: %v", err)
	}

	if item != nil {
		t.Error("Expected nil for non-existent key, got item")
	}
}

func testTTL(t *testing.T, s storage.Storage) {
	ctx := context.Background()
	key := "ttl-key"
	data := []byte("ttl data")
	ttl := 100 * time.Millisecond

	// Set data with TTL
	err := s.Set(ctx, key, data, storage.WithTTL(ttl))
	if err != nil {
		t.Fatalf("Failed to set data with TTL: %v", err)
	}

	// Get data immediately
	item, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}

	if item == nil {
		t.Fatal("Expected item to exist, got nil")
	}

	if item.ExpiresAt == nil {
		t.Fatal("ExpiresAt should not be nil for data with TTL")
	}

	// Wait for expiration
	time.Sleep(ttl + 50*time.Millisecond)

	// Try to get expired data
	item, err = s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get expired data: %v", err)
	}

	if item != nil {
		t.Error("Expected nil for expired data, got item")
	}
}

func testNamespaces(t *testing.T, s storage.Storage) {
	ctx := context.Background()
	key := "namespace-key"
	globalData := []byte("global data")
	userData := []byte("user data")
	sessionData := []byte("session data")

	// Set data in different namespaces
	err := s.Set(ctx, key, globalData)
	if err != nil {
		t.Fatalf("Failed to set global data: %v", err)
	}

	err = s.Set(ctx, key, userData, storage.WithUser("user1"))
	if err != nil {
		t.Fatalf("Failed to set user data: %v", err)
	}

	err = s.Set(ctx, key, sessionData, storage.WithUserSession("user1", "session1"))
	if err != nil {
		t.Fatalf("Failed to set session data: %v", err)
	}

	// Get data from different namespaces
	item, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get global data: %v", err)
	}
	if item == nil || string(item.Data) != string(globalData) {
		t.Errorf("Expected global data %s, got %v", globalData, item)
	}

	item, err = s.Get(ctx, key, storage.WithUser("user1"))
	if err != nil {
		t.Fatalf("Failed to get user data: %v", err)
	}
	if item == nil || string(item.Data) != string(userData) {
		t.Errorf("Expected user data %s, got %v", userData, item)
	}

	item, err = s.Get(ctx, key, storage.WithUserSession("user1", "session1"))
	if err != nil {
		t.Fatalf("Failed to get session data: %v", err)
	}
	if item == nil || string(item.Data) != string(sessionData) {
		t.Errorf("Expected session data %s, got %v", sessionData, item)
	}

	// Data in different namespaces should be isolated
	item, err = s.Get(ctx, key, storage.WithUser("user2"))
	if err != nil {
		t.Fatalf("Failed to get data for different user: %v", err)
	}
	if item != nil {
		t.Error("Expected nil for different user namespace, got item")
	}
}

func testDeleteKey(t *testing.T, s storage.Storage) {
	ctx := context.Background()
	key := "delete-key"
	data := []byte("delete data")

	// Set data
	err := s.Set(ctx, key, data)
	if err != nil {
		t.Fatalf("Failed to set data: %v", err)
	}

	// Verify data exists
	item, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}
	if item == nil {
		t.Fatal("Expected item to exist before deletion")
	}

	// Delete specific key
	err = s.Delete(ctx, storage.WithKey(key))
	if err != nil {
		t.Fatalf("Failed to delete key: %v", err)
	}

	// Verify data is gone
	item, err = s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get data after deletion: %v", err)
	}
	if item != nil {
		t.Error("Expected nil after deletion, got item")
	}
}

func testDeleteNamespace(t *testing.T, s storage.Storage) {
	ctx := context.Background()
	userID := "delete-user"

	// Set multiple keys in user namespace
	keys := []string{"key1", "key2", "key3"}
	for _, key := range keys {
		data := []byte("data for " + key)
		err := s.Set(ctx, key, data, storage.WithUser(userID))
		if err != nil {
			t.Fatalf("Failed to set data for key %s: %v", key, err)
		}
	}

	// Verify all keys exist
	for _, key := range keys {
		item, err := s.Get(ctx, key, storage.WithUser(userID))
		if err != nil {
			t.Fatalf("Failed to get data for key %s: %v", key, err)
		}
		if item == nil {
			t.Fatalf("Expected item to exist for key %s before deletion", key)
		}
	}

	// Delete entire user namespace
	err := s.Delete(ctx, storage.WithUser(userID))
	if err != nil {
		t.Fatalf("Failed to delete user namespace: %v", err)
	}

	// Verify all keys are gone
	for _, key := range keys {
		item, err := s.Get(ctx, key, storage.WithUser(userID))
		if err != nil {
			t.Fatalf("Failed to get data for key %s after deletion: %v", key, err)
		}
		if item != nil {
			t.Errorf("Expected nil after namespace deletion for key %s, got item", key)
		}
	}
}
