// Package storage provides an interface for managing hierarchical data
// in the MCP streaming HTTP transport implementation.
package storage

import (
	"context"
	"errors"
	"time"
)

// Storage defines the primary interface for hierarchical data storage
type Storage interface {
	// Get retrieves data for a specific key within the given namespace
	// Returns nil StorageItem if key doesn't exist or has expired
	// Returns error only for legitimate storage system failures
	Get(ctx context.Context, key string, opts ...Option) (*StorageItem, error)

	// Set stores data for a specific key within the given namespace
	Set(ctx context.Context, key string, data []byte, opts ...Option) error

	// Delete removes data within the given namespace
	// If no key specified via WithKey, removes entire namespace
	Delete(ctx context.Context, opts ...Option) error

	// Close closes the storage backend and releases resources
	Close() error
}

// StorageItem represents a stored piece of data with metadata
type StorageItem struct {
	Data      []byte     // The stored data
	CreatedAt time.Time  // When the item was created
	ExpiresAt *time.Time // When the item expires (nil = no expiration)
}

// IsExpired checks if the item has expired
func (si *StorageItem) IsExpired() bool {
	return si.ExpiresAt != nil && time.Now().After(*si.ExpiresAt)
}

// Option configures storage operations
type Option func(*Options)

// Options contains configuration for storage operations
type Options struct {
	Namespace Namespace      // Optional: specifies the storage namespace (nil = global)
	Key       *string        // Optional: specific key (for Delete operations)
	TTL       *time.Duration // Optional: time-to-live for the data
}

// Namespace represents a storage namespace (user or session level)
// If nil, storage operates in global namespace
type Namespace interface {
	namespace() // private method to ensure only our types implement this
}

// UserNamespace represents user-level storage
type UserNamespace struct {
	UserID string
}

func (UserNamespace) namespace() {}

// SessionNamespace represents session-level storage
type SessionNamespace struct {
	UserID    string
	SessionID string
}

func (SessionNamespace) namespace() {}

// WithUser specifies user-level storage namespace
func WithUser(userID string) Option {
	return func(opts *Options) {
		opts.Namespace = UserNamespace{UserID: userID}
	}
}

// WithUserSession specifies session-level storage namespace
func WithUserSession(userID, sessionID string) Option {
	return func(opts *Options) {
		opts.Namespace = SessionNamespace{UserID: userID, SessionID: sessionID}
	}
}

// WithKey specifies a specific key for Delete operations
// If not provided, Delete removes the entire namespace
func WithKey(key string) Option {
	return func(opts *Options) {
		opts.Key = &key
	}
}

// WithTTL sets a time-to-live for the stored data
func WithTTL(ttl time.Duration) Option {
	return func(opts *Options) {
		opts.TTL = &ttl
	}
}

// Error types
var (
	// ErrInvalidOptions is returned when incompatible options are provided
	ErrInvalidOptions = errors.New("storage: invalid option combination")
)
