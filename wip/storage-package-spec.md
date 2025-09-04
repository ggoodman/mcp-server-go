# Storage Package Specification

## Overview

The `storage` package provides an interface for managing hierarchical data in the MCP streaming HTTP transport implementation. The hierarchy follows a two-level structure: **Users** contain many **Sessions**, with data storage capabilities at both levels.

## Hierarchy

```
User (ID: string)
├── User-level data (key-value pairs)
└── Sessions (ID: string)
    └── Session-level data (key-value pairs)
```

## Core Interface

### Storage Interface

```go
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
```

## Data Types

### StorageItem

```go
// StorageItem represents a stored piece of data with metadata
type StorageItem struct {
    Data      []byte    // The stored data
    CreatedAt time.Time // When the item was created
    ExpiresAt *time.Time // When the item expires (nil = no expiration)
}

// IsExpired checks if the item has expired
func (si *StorageItem) IsExpired() bool {
    return si.ExpiresAt != nil && time.Now().After(*si.ExpiresAt)
}
```

### Option Types and Namespaces

```go
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
```

## Error Types

```go
var (
    // ErrInvalidOptions is returned when incompatible options are provided
    ErrInvalidOptions = errors.New("storage: invalid option combination")
)
```

## Implementation Considerations

### Memory Implementation

A memory-based implementation should be provided for:

- Development/testing scenarios
- Single-node deployments
- Embedded use cases

Key considerations:

- Thread-safe operations using sync.RWMutex
- Automatic cleanup of expired items
- Graceful handling of memory pressure

### Distributed Implementation Readiness

The interface should support future distributed implementations:

- Redis/KeyDB for distributed caching
- Database backends (PostgreSQL, MySQL)
- Cloud storage solutions (AWS DynamoDB, GCP Firestore)

### Key Design Principles

1. **Unified Interface**: Single Get/Set/Delete methods with namespace options
2. **Type-Safe Namespaces**: Compile-time safety for user vs session storage
3. **Global Storage Support**: Operations without namespace options use global storage
4. **Namespace Isolation**: Complete isolation between different users and sessions
5. **Optional TTL**: Expiration support via WithTTL option
6. **Context Support**: All operations support context for cancellation and timeouts
7. **Error Clarity**: Clear error types for different failure scenarios

### Security Considerations

- User IDs and Session IDs should be treated as opaque strings
- No cross-user data leakage under any circumstances
- Input validation for IDs and keys to prevent injection attacks
- Sanitization of user-controlled data

### Performance Considerations

- Efficient key lookup structures
- Bulk operations for cleanup (expired items, user deletion)
- Minimal memory overhead for metadata
- Support for concurrent operations

## Usage Examples

### Basic Operations

```go
// Store global data with TTL
err := storage.Set(ctx, "global-config", []byte(`{"version": "1.0"}`),
    WithTTL(24*time.Hour))

// Store user-level data with TTL
err := storage.Set(ctx, "preferences", []byte(`{"theme": "dark"}`),
    WithUser("user123"), WithTTL(24*time.Hour))

// Retrieve user-level data
item, err := storage.Get(ctx, "preferences", WithUser("user123"))
if err != nil {
    // Only storage system errors, not "not found"
    return err
}
if item == nil {
    // Key doesn't exist or has expired
    fmt.Println("No preferences found")
    return
}

// Retrieve global data
item, err := storage.Get(ctx, "global-config")
if err != nil {
    return err
}

// Store session-level data
err = storage.Set(ctx, "state", []byte(`{"last_action": "connect"}`),
    WithUserSession("user123", "session456"))

// Delete specific key in session
err = storage.Delete(ctx, WithUserSession("user123", "session456"), WithKey("state"))

// Delete entire session (all keys)
err = storage.Delete(ctx, WithUserSession("user123", "session456"))

// Delete entire user (all sessions and user data)
err = storage.Delete(ctx, WithUser("user123"))

// Delete specific global key
err = storage.Delete(ctx, WithKey("global-config"))
```

### Integration with MCP Handler

```go
type HandlerOptions struct {
    Storage storage.Storage
    // ... other options
}

func NewHandler(opts HandlerOptions) *Handler {
    return &Handler{
        storage: opts.Storage,
        // ... other fields
    }
}
```

## Open Questions

1. **Key Constraints**: Should we impose length or character constraints on user IDs, session IDs, and keys?

2. **Atomic Operations**: Do we need atomic compare-and-swap or transaction support?

3. **Monitoring**: Should the interface include metrics/observability hooks?

4. **Compression**: Should large data items be automatically compressed?

5. **Bulk Operations**: Are there use cases for bulk get/set/delete operations?

6. **Event Hooks**: Should storage operations trigger configurable callbacks?

## Next Steps

1. Implement memory-based storage in `storage/memory/`
2. Add comprehensive tests covering edge cases and concurrency
3. Create integration examples with the main MCP handler
4. Document migration patterns for different storage backends
5. Performance benchmarking and optimization
