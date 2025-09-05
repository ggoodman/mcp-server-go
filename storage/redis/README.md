# Redis Storage

Redis implementation of the `storage.Storage` interface for hierarchical key-value storage with TTL support.

## Features

- **Hierarchical Namespaces**: Support for global, user, and session-level storage
- **TTL Support**: Automatic expiration of stored data
- **JSON Serialization**: Automatic marshaling/unmarshaling of storage metadata
- **Efficient Scanning**: Uses Redis SCAN for namespace deletion
- **Connection Pooling**: Leverages Redis client connection pooling

## Usage

```go
import (
    "github.com/ggoodman/mcp-streaming-http-go/storage/redis"
    "github.com/redis/go-redis/v9"
)

// Create Redis client
client := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
})

// Create storage with default configuration
storage, err := redis.New(redis.Config{
    Client: client,
})

// Create storage with custom configuration
storage, err := redis.New(redis.Config{
    Client:    client,
    KeyPrefix: "myapp:storage:",  // Custom key prefix
})
```

## Configuration

| Option      | Default        | Description               |
| ----------- | -------------- | ------------------------- |
| `Client`    | required       | Redis client instance     |
| `KeyPrefix` | "mcp:storage:" | Prefix for all Redis keys |

## Redis Key Structure

The storage implementation uses a hierarchical key structure:

- Global: `{KeyPrefix}global:{key}`
- User: `{KeyPrefix}user:{userID}:{key}`
- Session: `{KeyPrefix}session:{userID}:{sessionID}:{key}`

## Storage Operations

### Set with TTL

```go
// Store data that expires in 1 hour
err := storage.Set(ctx, "session-token", tokenData,
    storage.WithUser("user123"),
    storage.WithTTL(time.Hour))
```

### Namespace Operations

```go
// Global namespace
item, err := storage.Get(ctx, "config-key")

// User namespace
item, err := storage.Get(ctx, "preferences", storage.WithUser("user123"))

// Session namespace
item, err := storage.Get(ctx, "temp-data",
    storage.WithUserSession("user123", "session456"))
```

### Deletion

```go
// Delete specific key
err := storage.Delete(ctx, storage.WithUser("user123"), storage.WithKey("preferences"))

// Delete entire user namespace
err := storage.Delete(ctx, storage.WithUser("user123"))

// Delete entire session namespace
err := storage.Delete(ctx, storage.WithUserSession("user123", "session456"))
```

## Data Format

Data is stored in Redis as JSON with metadata:

```json
{
  "data": "base64-encoded-or-string-data",
  "created_at": "2023-12-01T12:00:00Z",
  "expires_at": "2023-12-01T13:00:00Z" // optional
}
```

## TTL Behavior

- Redis native TTL is used for automatic expiration
- Application-level expiration checking provides additional safety
- Expired items are automatically removed when accessed

## Requirements

- Redis 2.8+ (for basic operations)
- Redis 2.8+ (for SCAN command support)
- Network connectivity between all application instances and Redis
