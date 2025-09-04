package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/storage"
	"github.com/ggoodman/mcp-streaming-http-go/storage/memory"
)

func main() {
	// Create a new in-memory storage with max 1000 items
	store, err := memory.New(1000)
	if err != nil {
		log.Fatal("Failed to create storage:", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Example 1: Global storage
	fmt.Println("=== Global Storage ===")
	err = store.Set(ctx, "app-config", []byte(`{"version": "1.0", "debug": true}`))
	if err != nil {
		log.Fatal("Failed to set global config:", err)
	}

	item, err := store.Get(ctx, "app-config")
	if err != nil {
		log.Fatal("Failed to get global config:", err)
	}
	if item != nil {
		fmt.Printf("Global config: %s\n", string(item.Data))
	}

	// Example 2: User-level storage
	fmt.Println("\n=== User Storage ===")
	userID := "user123"
	err = store.Set(ctx, "preferences", []byte(`{"theme": "dark", "language": "en"}`),
		storage.WithUser(userID))
	if err != nil {
		log.Fatal("Failed to set user preferences:", err)
	}

	item, err = store.Get(ctx, "preferences", storage.WithUser(userID))
	if err != nil {
		log.Fatal("Failed to get user preferences:", err)
	}
	if item != nil {
		fmt.Printf("User %s preferences: %s\n", userID, string(item.Data))
	}

	// Example 3: Session-level storage
	fmt.Println("\n=== Session Storage ===")
	sessionID := "session456"
	err = store.Set(ctx, "state", []byte(`{"last_action": "login", "timestamp": "2025-09-03T12:00:00Z"}`),
		storage.WithUserSession(userID, sessionID))
	if err != nil {
		log.Fatal("Failed to set session state:", err)
	}

	item, err = store.Get(ctx, "state", storage.WithUserSession(userID, sessionID))
	if err != nil {
		log.Fatal("Failed to get session state:", err)
	}
	if item != nil {
		fmt.Printf("Session %s state: %s\n", sessionID, string(item.Data))
	}

	// Example 4: TTL (Time-to-Live)
	fmt.Println("\n=== TTL Example ===")
	err = store.Set(ctx, "temp-data", []byte("This will expire soon"),
		storage.WithUser(userID), storage.WithTTL(2*time.Second))
	if err != nil {
		log.Fatal("Failed to set temp data:", err)
	}

	// Get immediately
	item, err = store.Get(ctx, "temp-data", storage.WithUser(userID))
	if err != nil {
		log.Fatal("Failed to get temp data:", err)
	}
	if item != nil {
		fmt.Printf("Temp data (immediate): %s\n", string(item.Data))
	}

	// Wait and try again
	fmt.Println("Waiting 3 seconds for TTL expiration...")
	time.Sleep(3 * time.Second)

	item, err = store.Get(ctx, "temp-data", storage.WithUser(userID))
	if err != nil {
		log.Fatal("Failed to get temp data after expiration:", err)
	}
	if item == nil {
		fmt.Println("Temp data expired (as expected)")
	}

	// Example 5: Namespace isolation
	fmt.Println("\n=== Namespace Isolation ===")
	key := "same-key"

	// Set same key in different namespaces
	store.Set(ctx, key, []byte("global-value"))
	store.Set(ctx, key, []byte("user-value"), storage.WithUser("user1"))
	store.Set(ctx, key, []byte("session-value"), storage.WithUserSession("user1", "session1"))

	// Retrieve from each namespace
	global, _ := store.Get(ctx, key)
	user, _ := store.Get(ctx, key, storage.WithUser("user1"))
	session, _ := store.Get(ctx, key, storage.WithUserSession("user1", "session1"))

	fmt.Printf("Global '%s': %s\n", key, string(global.Data))
	fmt.Printf("User '%s': %s\n", key, string(user.Data))
	fmt.Printf("Session '%s': %s\n", key, string(session.Data))

	// Example 6: Deletion
	fmt.Println("\n=== Deletion Examples ===")

	// Delete specific key
	err = store.Delete(ctx, storage.WithUser("user1"), storage.WithKey(key))
	if err != nil {
		log.Fatal("Failed to delete user key:", err)
	}
	fmt.Println("Deleted user key")

	// Delete entire session
	err = store.Delete(ctx, storage.WithUserSession("user1", "session1"))
	if err != nil {
		log.Fatal("Failed to delete session:", err)
	}
	fmt.Println("Deleted entire session")

	// Verify deletions
	user, _ = store.Get(ctx, key, storage.WithUser("user1"))
	session, _ = store.Get(ctx, key, storage.WithUserSession("user1", "session1"))

	if user == nil {
		fmt.Println("User key successfully deleted")
	}
	if session == nil {
		fmt.Println("Session data successfully deleted")
	}

	// Global should still exist
	global, _ = store.Get(ctx, key)
	if global != nil {
		fmt.Printf("Global '%s' still exists: %s\n", key, string(global.Data))
	}
}
