# Broker and Storage Abstraction Refactoring

## Overview

This document outlines a refactoring of the session management architecture to introduce two new abstractions: **Broker** and **Storage**. This change will improve separation of concerns, enable horizontal scalability, and make the system more testable while maintaining backward compatibility.

## Current Architecture Problems

### 1. Mixed Responsibilities

The current `memory.Store` handles multiple concerns:

- Message queuing and delivery (channels, subscribers)
- Session metadata persistence (user IDs, timestamps, capabilities)
- Session lifecycle management
- Cleanup and TTL logic

### 2. Scalability Limitations

- Tightly coupled to in-memory, single-instance implementation
- Difficult to transition to distributed messaging (Redis Streams, etc.)
- Message delivery and persistence are bundled together

### 3. Testing Complexity

- Mocking requires understanding entire `SessionManager` interface
- Cannot test message delivery separately from persistence
- Difficult to test failure scenarios across different concerns

## Proposed Architecture

### New Abstractions

#### Broker Interface

Responsible for message queuing, delivery, and event ID generation:

```go
// Broker handles message queuing and delivery concerns
type Broker interface {
    // Publish creates envelope with generated event ID and publishes to session
    // Returns the generated event ID for the published message
    Publish(ctx context.Context, sessionID string, message jsonrpc.Message) (eventID string, error)

    // Subscribe to session messages, resuming from lastEventID if provided
    Subscribe(ctx context.Context, sessionID string, lastEventID string) (MessageStream, error)

    // Clean up all resources associated with a session
    CleanupSession(ctx context.Context, sessionID string) error
}

// MessageStream provides ordered message consumption
type MessageStream interface {
    // Next blocks until the next message is available or context is cancelled
    Next(ctx context.Context) (MessageEnvelope, error)

    // Close releases resources associated with this stream
    Close() error
}
```

#### Storage Interface

Responsible for session metadata persistence:

```go
// Storage handles persistence of session metadata
type Storage interface {
    // CreateSession stores new session metadata
    CreateSession(ctx context.Context, sessionID string, userID string, metadata SessionMetadata) error

    // GetSession retrieves session metadata, returns ErrSessionNotFound if not found
    GetSession(ctx context.Context, sessionID string, userID string) (*StoredSession, error)

    // TouchSession updates the last_seen_at timestamp
    TouchSession(ctx context.Context, sessionID string) error

    // DeleteSession removes session metadata
    DeleteSession(ctx context.Context, sessionID string) error

    // GetExpiredSessions returns session IDs that haven't been seen within the TTL
    GetExpiredSessions(ctx context.Context, ttl time.Duration) ([]string, error)
}

// StoredSession represents persisted session metadata
type StoredSession struct {
    SessionID string
    UserID    string
    Metadata  SessionMetadata
    CreatedAt time.Time
    LastSeenAt time.Time
}
```

### Concrete Implementations

#### SessionManager (Concrete)

Coordinates Broker and Storage, replacing the current interface:

```go
// SessionManager coordinates message delivery and session persistence
type SessionManager struct {
    broker  Broker
    storage Storage
    logger  *slog.Logger
}

func NewSessionManager(broker Broker, storage Storage, logger *slog.Logger) *SessionManager

func (sm *SessionManager) CreateSession(ctx context.Context, userID string, meta SessionMetadata) (*Session, error)
func (sm *SessionManager) LoadSession(ctx context.Context, sessionID string, userID string) (*Session, error)
func (sm *SessionManager) StartCleanup(ctx context.Context, ttl time.Duration) error
```

#### Session (Concrete)

Provides session operations, coordinating between Broker and Storage:

```go
// Session provides session-scoped operations
type Session struct {
    sessionID string
    userID    string
    metadata  SessionMetadata
    broker    Broker
    storage   Storage
}

func (s *Session) SessionID() string
func (s *Session) UserID() string
func (s *Session) ConsumeMessages(ctx context.Context, lastEventID string, writeMsgFn MessageHandlerFunction) error
func (s *Session) PublishMessage(ctx context.Context, message jsonrpc.Message) (eventID string, error)
func (s *Session) GetSamplingCapability() hooks.SamplingCapability
func (s *Session) GetRootsCapability() hooks.RootsCapability
func (s *Session) GetElicitationCapability() hooks.ElicitationCapability
```

### Implementation Packages

#### 1. Channel Broker (`broker/channel`)

Single-instance implementation using Go channels:

```go
type ChannelBroker struct {
    sessions map[string]*sessionState
    mu       sync.RWMutex
    idGen    *atomic.Int64
}

type sessionState struct {
    subscribers map[string]chan MessageEnvelope
    messages    []MessageEnvelope // For cursor support
    mu          sync.RWMutex
}
```

**Features:**

- Atomic counter for event IDs
- In-memory message buffering for cursor-based consumption
- Automatic cleanup of disconnected subscribers

#### 2. Redis Broker (`broker/redis`)

Distributed implementation using Redis Streams:

```go
type RedisBroker struct {
    client redis.UniversalClient
    config BrokerConfig
}

type BrokerConfig struct {
    StreamMaxLen int
    ReadTimeout  time.Duration
}
```

**Features:**

- Uses Redis Stream IDs as event IDs
- Leverages XREAD for cursor-based consumption
- Automatic message trimming with MAXLEN
- Consumer groups for horizontal scaling

#### 3. Memory Storage (`storage/memory`)

Single-instance implementation:

```go
type MemoryStorage struct {
    sessions map[string]*StoredSession
    mu       sync.RWMutex
}
```

#### 4. SQL Storage (`storage/sql`)

Database-backed implementation:

```go
type SQLStorage struct {
    db     *sql.DB
    driver string
}
```

**Schema:**

```sql
CREATE TABLE sessions (
    session_id VARCHAR(255) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    metadata JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    last_seen_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    INDEX idx_user_sessions (user_id),
    INDEX idx_last_seen (last_seen_at)
);
```

## Migration Strategy

### Phase 1: Interface Extraction

- [ ] Create new `Broker` and `Storage` interfaces
- [ ] Keep existing `SessionManager` interface for backward compatibility
- [ ] Create adapter that wraps current `memory.Store` to implement new interfaces

### Phase 2: Concrete Implementations

- [ ] Implement concrete `SessionManager` and `Session` types
- [ ] Implement `ChannelBroker` wrapping current channel logic
- [ ] Implement `MemoryStorage` wrapping current metadata logic
- [ ] Update handler to use concrete types instead of interfaces

### Phase 3: New Implementations

- [ ] Implement `RedisBroker` for distributed messaging
- [ ] Implement `SQLStorage` for persistent metadata
- [ ] Add configuration options for different combinations

### Phase 4: Cleanup

- [ ] Remove old `SessionManager` interface
- [ ] Remove old `Session` interface
- [ ] Remove `memory.Store` implementation
- [ ] Update documentation and examples

## Error Handling Strategy

### Coordination Errors

When operations span both Broker and Storage, implement compensation patterns:

```go
func (sm *SessionManager) CreateSession(ctx context.Context, userID string, meta SessionMetadata) (*Session, error) {
    sessionID := generateSessionID()

    // Create storage entry first
    if err := sm.storage.CreateSession(ctx, sessionID, userID, meta); err != nil {
        return nil, fmt.Errorf("failed to create session in storage: %w", err)
    }

    // If broker setup fails, clean up storage
    session := &Session{sessionID: sessionID, userID: userID, metadata: meta, broker: sm.broker, storage: sm.storage}

    return session, nil
}
```

### Failure Scenarios

- **Storage failure during CreateSession**: Return error, no cleanup needed
- **Broker failure during Publish**: Return error, message not delivered
- **Storage failure during LoadSession**: Return `ErrSessionNotFound`
- **Network partitions**: Broker and Storage operate independently, eventual consistency

## Performance Considerations

### Memory Usage

- **ChannelBroker**: Configurable buffer sizes, automatic cleanup
- **RedisBroker**: Configurable stream trimming to prevent unbounded growth
- **Storage**: Connection pooling, prepared statements

### Latency

- **Publish operations**: Single round-trip to Broker + Storage
- **Subscribe operations**: Long-polling or streaming from Broker
- **Session lookup**: Single round-trip to Storage with caching potential

### Scalability

- **Horizontal scaling**: Multiple handler instances can share Redis Broker + SQL Storage
- **Vertical scaling**: Independent scaling of Broker and Storage resources
- **Geographic distribution**: Regional Broker instances with global Storage

## Testing Strategy

### Unit Testing

```go
func TestSessionManager_CreateSession(t *testing.T) {
    broker := &MockBroker{}
    storage := &MockStorage{}
    sm := NewSessionManager(broker, storage, slog.Default())

    // Test successful creation
    session, err := sm.CreateSession(ctx, "user123", metadata)
    assert.NoError(t, err)
    assert.Equal(t, "user123", session.UserID())

    // Verify storage was called
    storage.AssertCalled(t, "CreateSession", mock.Anything, mock.Anything, "user123", metadata)
}
```

### Integration Testing

```go
func TestFullStackIntegration(t *testing.T) {
    // Test with real ChannelBroker + MemoryStorage
    broker := channel.NewBroker()
    storage := memory.NewStorage()
    sm := NewSessionManager(broker, storage, slog.Default())

    // Test complete session lifecycle
    session, err := sm.CreateSession(ctx, "user123", metadata)
    require.NoError(t, err)

    // Test message publishing and consumption
    eventID, err := session.PublishMessage(ctx, testMessage)
    require.NoError(t, err)

    // Test message consumption with cursor
    consumed := false
    err = session.ConsumeMessages(ctx, "", func(ctx context.Context, env MessageEnvelope) error {
        assert.Equal(t, eventID, env.MessageID)
        consumed = true
        return nil
    })
    assert.True(t, consumed)
}
```

### Load Testing

- **Message throughput**: Publish rate per session, total system throughput
- **Concurrent sessions**: Memory usage and performance with many active sessions
- **Cursor performance**: Resume speed with large message backlogs
- **Cleanup efficiency**: Background cleanup impact on active sessions

## Configuration

### Environment-Based Configuration

```go
type Config struct {
    Broker struct {
        Type   string `env:"BROKER_TYPE" envDefault:"channel"` // "channel" or "redis"
        Redis  RedisBrokerConfig
        Channel ChannelBrokerConfig
    }
    Storage struct {
        Type   string `env:"STORAGE_TYPE" envDefault:"memory"` // "memory" or "sql"
        SQL    SQLStorageConfig
        Memory MemoryStorageConfig
    }
    SessionTTL time.Duration `env:"SESSION_TTL" envDefault:"30m"`
}
```

### Factory Pattern

```go
func NewSessionManagerFromConfig(config Config, logger *slog.Logger) (*SessionManager, error) {
    broker, err := createBroker(config.Broker)
    if err != nil {
        return nil, fmt.Errorf("failed to create broker: %w", err)
    }

    storage, err := createStorage(config.Storage)
    if err != nil {
        return nil, fmt.Errorf("failed to create storage: %w", err)
    }

    return NewSessionManager(broker, storage, logger), nil
}
```

## TODO List

### Phase 1: Interface Extraction (Week 1)

- [ ] Create `broker/broker.go` with `Broker` and `MessageStream` interfaces
- [ ] Create `storage/storage.go` with `Storage` interface and `StoredSession` struct
- [ ] Create `sessions/manager.go` with concrete `SessionManager` struct
- [ ] Create `sessions/session.go` with concrete `Session` struct
- [ ] Add backward compatibility adapter in `sessions/compat.go`
- [ ] Update `handler.go` to use concrete `SessionManager` instead of interface
- [ ] Ensure all existing tests pass with new architecture

### Phase 2: Channel Broker Implementation (Week 2)

- [ ] Create `broker/channel/broker.go` implementing `Broker` interface
- [ ] Implement atomic event ID generation (timestamp-based or counter)
- [ ] Implement message buffering for cursor-based consumption
- [ ] Implement subscriber management and cleanup
- [ ] Add comprehensive unit tests
- [ ] Add benchmarks for message throughput

### Phase 3: Memory Storage Implementation (Week 2)

- [ ] Create `storage/memory/storage.go` implementing `Storage` interface
- [ ] Implement session CRUD operations with proper locking
- [ ] Implement TTL-based session expiration
- [ ] Add comprehensive unit tests
- [ ] Add benchmarks for concurrent access patterns

### Phase 4: Integration and Testing (Week 3)

- [ ] Wire up `ChannelBroker` and `MemoryStorage` in `SessionManager`
- [ ] Update all handler tests to use new concrete types
- [ ] Add full integration tests covering complete session lifecycle
- [ ] Add load tests for concurrent sessions and message throughput
- [ ] Verify performance is equivalent to current implementation
- [ ] Update documentation and examples

### Phase 5: Redis Broker Implementation (Week 4)

- [ ] Create `broker/redis/broker.go` implementing `Broker` interface
- [ ] Implement Redis Streams-based message publishing
- [ ] Implement cursor-based consumption using XREAD
- [ ] Add Redis connection management and error handling
- [ ] Add configuration options (stream max length, timeouts)
- [ ] Add comprehensive unit and integration tests
- [ ] Add Redis-specific benchmarks and load tests

### Phase 6: SQL Storage Implementation (Week 5)

- [ ] Create `storage/sql/storage.go` implementing `Storage` interface
- [ ] Design and implement database schema
- [ ] Add support for PostgreSQL and SQLite drivers
- [ ] Implement connection pooling and prepared statements
- [ ] Add database migration support
- [ ] Add comprehensive unit and integration tests
- [ ] Add database-specific benchmarks

### Phase 7: Configuration and Documentation (Week 6)

- [ ] Add environment-based configuration system
- [ ] Implement factory pattern for creating components from config
- [ ] Add Docker Compose examples for different configurations
- [ ] Write comprehensive README sections for new architecture
- [ ] Add API documentation for all new interfaces
- [ ] Create migration guide from old to new architecture
- [ ] Add troubleshooting guide for common issues

### Phase 8: Production Readiness (Week 7)

- [ ] Add structured logging throughout all components
- [ ] Add Prometheus metrics for key operations
- [ ] Add health check endpoints for Broker and Storage
- [ ] Add graceful shutdown handling
- [ ] Add circuit breaker patterns for external dependencies
- [ ] Performance optimization based on benchmarks
- [ ] Add end-to-end tests simulating production scenarios

### Phase 9: Cleanup and Release (Week 8)

- [ ] Remove deprecated `SessionManager` interface
- [ ] Remove deprecated `Session` interface
- [ ] Remove old `memory.Store` implementation
- [ ] Update all import paths and examples
- [ ] Tag release with breaking change notes
- [ ] Write blog post about architecture improvements
- [ ] Update project README with new capabilities

## Success Criteria

### Functional Requirements

- [ ] All existing functionality preserved
- [ ] No performance regression for single-instance use case
- [ ] Horizontal scalability demonstrated with Redis+SQL implementation
- [ ] 100% test coverage for new interfaces and core implementations

### Non-Functional Requirements

- [ ] Sub-10ms latency for message publishing (95th percentile)
- [ ] Support for 10,000+ concurrent sessions per instance
- [ ] Memory usage remains bounded with configurable limits
- [ ] Zero message loss during normal operations
- [ ] Graceful degradation during partial system failures

### Developer Experience

- [ ] Clear documentation for all configuration options
- [ ] Easy migration path from existing implementations
- [ ] Comprehensive examples for different deployment scenarios
- [ ] Good error messages with actionable guidance
