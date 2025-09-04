// Package memory provides an in-memory implementation of the storage interface
// using github.com/hashicorp/golang-lru/v2 for efficient caching with TTL support.
package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/storage"
	lru "github.com/hashicorp/golang-lru/v2"
)

// Storage implements the storage.Storage interface using in-memory storage
type Storage struct {
	mu    sync.RWMutex
	cache *lru.Cache[string, *storage.StorageItem]
}

// New creates a new in-memory storage implementation
func New(maxItems int) (*Storage, error) {
	cache, err := lru.New[string, *storage.StorageItem](maxItems)
	if err != nil {
		return nil, fmt.Errorf("failed to create LRU cache: %w", err)
	}

	s := &Storage{
		cache: cache,
	}

	// Start background cleanup of expired items
	go s.cleanupExpired()

	return s, nil
}

// Get retrieves data for a specific key within the given namespace
func (s *Storage) Get(ctx context.Context, key string, opts ...storage.Option) (*storage.StorageItem, error) {
	// Parse options
	options := &storage.Options{}
	for _, opt := range opts {
		opt(options)
	}

	// Build storage key
	storageKey := s.buildKey(options.Namespace, key)

	s.mu.RLock()
	item, exists := s.cache.Get(storageKey)
	s.mu.RUnlock()

	if !exists {
		return nil, nil
	}

	// Check if item has expired
	if item.IsExpired() {
		// Remove expired item and return nil
		s.mu.Lock()
		s.cache.Remove(storageKey)
		s.mu.Unlock()
		return nil, nil
	}

	return item, nil
}

// Set stores data for a specific key within the given namespace
func (s *Storage) Set(ctx context.Context, key string, data []byte, opts ...storage.Option) error {
	// Parse options
	options := &storage.Options{}
	for _, opt := range opts {
		opt(options)
	}

	// Build storage key
	storageKey := s.buildKey(options.Namespace, key)

	// Create storage item
	now := time.Now()
	item := &storage.StorageItem{
		Data:      make([]byte, len(data)),
		CreatedAt: now,
	}
	copy(item.Data, data)

	// Set expiration if TTL provided
	if options.TTL != nil {
		expiresAt := now.Add(*options.TTL)
		item.ExpiresAt = &expiresAt
	}

	s.mu.Lock()
	s.cache.Add(storageKey, item)
	s.mu.Unlock()

	return nil
}

// Delete removes data within the given namespace
func (s *Storage) Delete(ctx context.Context, opts ...storage.Option) error {
	// Parse options
	options := &storage.Options{}
	for _, opt := range opts {
		opt(options)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if options.Key != nil {
		// Delete specific key
		storageKey := s.buildKey(options.Namespace, *options.Key)
		s.cache.Remove(storageKey)
	} else {
		// Delete entire namespace
		prefix := s.buildNamespacePrefix(options.Namespace)
		s.deleteByPrefix(prefix)
	}

	return nil
}

// Close closes the storage backend and releases resources
func (s *Storage) Close() error {
	s.mu.Lock()
	s.cache.Purge()
	s.mu.Unlock()
	return nil
}

// buildKey creates a storage key from namespace and key
func (s *Storage) buildKey(namespace storage.Namespace, key string) string {
	switch ns := namespace.(type) {
	case storage.UserNamespace:
		return fmt.Sprintf("user:%s:key:%s", ns.UserID, key)
	case storage.SessionNamespace:
		return fmt.Sprintf("user:%s:session:%s:key:%s", ns.UserID, ns.SessionID, key)
	case nil:
		return fmt.Sprintf("global:key:%s", key)
	default:
		// This should never happen due to the private namespace() method
		return fmt.Sprintf("unknown:key:%s", key)
	}
}

// buildNamespacePrefix creates a prefix for namespace-based operations
func (s *Storage) buildNamespacePrefix(namespace storage.Namespace) string {
	switch ns := namespace.(type) {
	case storage.UserNamespace:
		return fmt.Sprintf("user:%s:", ns.UserID)
	case storage.SessionNamespace:
		return fmt.Sprintf("user:%s:session:%s:", ns.UserID, ns.SessionID)
	case nil:
		return "global:"
	default:
		// This should never happen due to the private namespace() method
		return "unknown:"
	}
}

// deleteByPrefix removes all keys with the given prefix
func (s *Storage) deleteByPrefix(prefix string) {
	// Get all keys (this is inefficient but LRU doesn't provide prefix iteration)
	keys := s.cache.Keys()

	for _, key := range keys {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			s.cache.Remove(key)
		}
	}
}

// cleanupExpired runs a background goroutine to periodically clean up expired items
func (s *Storage) cleanupExpired() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.Lock()
		keys := s.cache.Keys()
		now := time.Now()

		for _, key := range keys {
			if item, exists := s.cache.Peek(key); exists {
				if item.ExpiresAt != nil && now.After(*item.ExpiresAt) {
					s.cache.Remove(key)
				}
			}
		}
		s.mu.Unlock()
	}
}
