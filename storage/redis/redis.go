// Package redis provides a Redis-based implementation of the storage.Storage interface
// using Redis for hierarchical key-value storage with TTL support.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/storage"
	"github.com/redis/go-redis/v9"
)

// Config contains configuration options for the Redis storage
type Config struct {
	// Client is the Redis client instance
	Client *redis.Client

	// KeyPrefix is the prefix for all Redis keys
	// Default: "mcp:storage:"
	KeyPrefix string
}

// Storage implements the storage.Storage interface using Redis
type Storage struct {
	client    *redis.Client
	keyPrefix string
}

// storedItem represents the structure stored in Redis
type storedItem struct {
	Data      []byte     `json:"data"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// New creates a new Redis-based storage instance.
func New(config Config) (*Storage, error) {
	if config.Client == nil {
		return nil, fmt.Errorf("redis client is required")
	}

	// Apply defaults
	if config.KeyPrefix == "" {
		config.KeyPrefix = "mcp:storage:"
	}

	return &Storage{
		client:    config.Client,
		keyPrefix: config.KeyPrefix,
	}, nil
}

// Get retrieves data for a specific key within the given namespace
func (s *Storage) Get(ctx context.Context, key string, opts ...storage.Option) (*storage.StorageItem, error) {
	// Parse options
	options := &storage.Options{}
	for _, opt := range opts {
		opt(options)
	}

	// Build Redis key
	redisKey := s.buildKey(options.Namespace, key)

	// Get data from Redis
	result := s.client.Get(ctx, redisKey)
	if result.Err() != nil {
		if result.Err() == redis.Nil {
			return nil, nil // Key doesn't exist
		}
		return nil, fmt.Errorf("failed to get key %s: %w", redisKey, result.Err())
	}

	// Unmarshal the stored item
	var item storedItem
	if err := json.Unmarshal([]byte(result.Val()), &item); err != nil {
		return nil, fmt.Errorf("failed to unmarshal stored data: %w", err)
	}

	// Convert to storage.StorageItem
	storageItem := &storage.StorageItem{
		Data:      item.Data,
		CreatedAt: item.CreatedAt,
		ExpiresAt: item.ExpiresAt,
	}

	// Check if expired
	if storageItem.IsExpired() {
		// Delete expired item
		s.client.Del(ctx, redisKey)
		return nil, nil
	}

	return storageItem, nil
}

// Set stores data for a specific key within the given namespace
func (s *Storage) Set(ctx context.Context, key string, data []byte, opts ...storage.Option) error {
	// Parse options
	options := &storage.Options{}
	for _, opt := range opts {
		opt(options)
	}

	// Build Redis key
	redisKey := s.buildKey(options.Namespace, key)

	// Create stored item
	now := time.Now()
	item := storedItem{
		Data:      data,
		CreatedAt: now,
	}

	// Set expiration if TTL is specified
	var redisTTL time.Duration
	if options.TTL != nil {
		expiresAt := now.Add(*options.TTL)
		item.ExpiresAt = &expiresAt
		redisTTL = *options.TTL
	}

	// Marshal the item
	itemData, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to marshal storage item: %w", err)
	}

	// Store in Redis
	result := s.client.Set(ctx, redisKey, itemData, redisTTL)
	if result.Err() != nil {
		return fmt.Errorf("failed to set key %s: %w", redisKey, result.Err())
	}

	return nil
}

// Delete removes data within the given namespace
func (s *Storage) Delete(ctx context.Context, opts ...storage.Option) error {
	// Parse options
	options := &storage.Options{}
	for _, opt := range opts {
		opt(options)
	}

	if options.Key != nil {
		// Delete specific key
		redisKey := s.buildKey(options.Namespace, *options.Key)
		result := s.client.Del(ctx, redisKey)
		if result.Err() != nil {
			return fmt.Errorf("failed to delete key %s: %w", redisKey, result.Err())
		}
	} else {
		// Delete entire namespace
		pattern := s.buildKey(options.Namespace, "*")

		// Use SCAN to find all keys matching the pattern
		keys, err := s.scanKeys(ctx, pattern)
		if err != nil {
			return fmt.Errorf("failed to scan keys for pattern %s: %w", pattern, err)
		}

		if len(keys) > 0 {
			// Delete all found keys
			result := s.client.Del(ctx, keys...)
			if result.Err() != nil {
				return fmt.Errorf("failed to delete keys: %w", result.Err())
			}
		}
	}

	return nil
}

// Close closes the storage backend and releases resources
func (s *Storage) Close() error {
	return s.client.Close()
}

// buildKey constructs the Redis key from namespace and key components
func (s *Storage) buildKey(namespace storage.Namespace, key string) string {
	if namespace == nil {
		return s.keyPrefix + "global:" + key
	}

	switch ns := namespace.(type) {
	case storage.UserNamespace:
		return s.keyPrefix + "user:" + ns.UserID + ":" + key
	case storage.SessionNamespace:
		return s.keyPrefix + "session:" + ns.UserID + ":" + ns.SessionID + ":" + key
	default:
		// Fallback to global namespace for unknown types
		return s.keyPrefix + "global:" + key
	}
}

// scanKeys uses Redis SCAN to find all keys matching a pattern
func (s *Storage) scanKeys(ctx context.Context, pattern string) ([]string, error) {
	var keys []string
	var cursor uint64

	for {
		result := s.client.Scan(ctx, cursor, pattern, 100) // Scan in batches of 100
		if result.Err() != nil {
			return nil, result.Err()
		}

		scanKeys, newCursor := result.Val()
		keys = append(keys, scanKeys...)
		cursor = newCursor

		if cursor == 0 {
			break
		}
	}

	return keys, nil
}

// Compile-time interface check
var _ storage.Storage = (*Storage)(nil)
