package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/hooks"
	"github.com/ggoodman/mcp-streaming-http-go/sessions"
	"github.com/google/uuid"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExpired  = errors.New("session expired")
)

// Store implements sessions.SessionStore using in-memory storage
// This is suitable for single-instance servers and testing
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	// Cleanup configuration
	sessionTTL    time.Duration
	cleanupTicker *time.Ticker
	cleanupDone   chan struct{}
}

// Session implements sessions.Session using channels for message passing
type Session struct {
	id           string
	userID       string
	metadata     sessions.SessionMetadata
	messages     chan sessions.MessageEnvelope
	subscribers  map[string]chan sessions.MessageEnvelope
	subMu        sync.RWMutex
	createdAt    time.Time
	lastAccessAt time.Time
	mu           sync.RWMutex
}

// StoreOption configures the memory store
type StoreOption func(*Store)

// WithSessionTTL sets the time-to-live for sessions
func WithSessionTTL(ttl time.Duration) StoreOption {
	return func(s *Store) {
		s.sessionTTL = ttl
	}
}

// NewStore creates a new memory-based session store
func NewStore(opts ...StoreOption) *Store {
	store := &Store{
		sessions:    make(map[string]*Session),
		sessionTTL:  30 * time.Minute, // Default 30 minutes
		cleanupDone: make(chan struct{}),
	}

	for _, opt := range opts {
		opt(store)
	}

	// Start cleanup goroutine
	store.cleanupTicker = time.NewTicker(store.sessionTTL / 4) // Clean up every quarter of the TTL
	go store.cleanupExpiredSessions()

	return store
}

// CreateSession implements sessions.SessionStore
func (s *Store) CreateSession(ctx context.Context, userID string, meta sessions.SessionMetadata) (sessions.Session, error) {
	sessionID := uuid.New().String()

	session := &Session{
		id:           sessionID,
		userID:       userID,
		metadata:     meta,
		messages:     make(chan sessions.MessageEnvelope, 100), // Buffered channel
		subscribers:  make(map[string]chan sessions.MessageEnvelope),
		createdAt:    time.Now(),
		lastAccessAt: time.Now(),
	}

	s.mu.Lock()
	s.sessions[sessionID] = session
	s.mu.Unlock()

	return session, nil
}

// LoadSession implements sessions.SessionStore
func (s *Store) LoadSession(ctx context.Context, sessID string, userID string) (sessions.Session, error) {
	s.mu.RLock()
	session, exists := s.sessions[sessID]
	s.mu.RUnlock()

	if !exists {
		return nil, ErrSessionNotFound
	}

	// Check if session belongs to the user
	if session.userID != userID {
		return nil, ErrSessionNotFound
	}

	// Update last access time
	session.mu.Lock()
	session.lastAccessAt = time.Now()
	session.mu.Unlock()

	return session, nil
}

// Close shuts down the store and cleans up resources
func (s *Store) Close() error {
	if s.cleanupTicker != nil {
		s.cleanupTicker.Stop()
	}
	close(s.cleanupDone)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Close all sessions
	for _, session := range s.sessions {
		session.close()
	}
	s.sessions = make(map[string]*Session)

	return nil
}

// cleanupExpiredSessions runs periodically to remove expired sessions
func (s *Store) cleanupExpiredSessions() {
	for {
		select {
		case <-s.cleanupTicker.C:
			s.performCleanup()
		case <-s.cleanupDone:
			return
		}
	}
}

func (s *Store) performCleanup() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	for sessionID, session := range s.sessions {
		session.mu.RLock()
		expired := now.Sub(session.lastAccessAt) > s.sessionTTL
		session.mu.RUnlock()

		if expired {
			session.close()
			delete(s.sessions, sessionID)
		}
	}
}

// SessionID implements sessions.Session
func (s *Session) SessionID() string {
	return s.id
}

// UserID implements sessions.Session
func (s *Session) UserID() string {
	return s.userID
}

// ConsumeMessages implements sessions.Session
func (s *Session) ConsumeMessages(ctx context.Context, writeMsgFn sessions.MessageHandlerFunction) error {
	// Create a subscriber channel for this consumption
	subID := uuid.New().String()
	subChan := make(chan sessions.MessageEnvelope, 10)

	s.subMu.Lock()
	s.subscribers[subID] = subChan
	s.subMu.Unlock()

	// Clean up subscriber when done
	defer func() {
		s.subMu.Lock()
		delete(s.subscribers, subID)
		close(subChan)
		s.subMu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case envelope, ok := <-subChan:
			if !ok {
				return nil // Channel closed
			}
			if err := writeMsgFn(ctx, envelope); err != nil {
				return err
			}
		}
	}
}

// SendMessage sends a message to all subscribers of this session
func (s *Session) SendMessage(envelope sessions.MessageEnvelope) {
	s.subMu.RLock()
	defer s.subMu.RUnlock()

	for _, subChan := range s.subscribers {
		select {
		case subChan <- envelope:
		default:
			// Subscriber channel is full, skip this subscriber
			// In a production system, you might want to log this
		}
	}
}

// GetSamplingCapability implements sessions.Session
func (s *Session) GetSamplingCapability() hooks.SamplingCapability {
	return s.metadata.GetSamplingCapability()
}

// GetRootsCapability implements sessions.Session
func (s *Session) GetRootsCapability() hooks.RootsCapability {
	return s.metadata.GetRootsCapability()
}

// GetElicitationCapability implements sessions.Session
func (s *Session) GetElicitationCapability() hooks.ElicitationCapability {
	return s.metadata.GetElicitationCapability()
}

// close closes the session and cleans up resources
func (s *Session) close() {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	// Close all subscriber channels
	for subID, subChan := range s.subscribers {
		close(subChan)
		delete(s.subscribers, subID)
	}

	// Close the main messages channel
	close(s.messages)
}

// GetAllSessions returns all active sessions (useful for testing)
func (s *Store) GetAllSessions() map[string]*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions := make(map[string]*Session, len(s.sessions))
	for k, v := range s.sessions {
		sessions[k] = v
	}
	return sessions
}

// GetSessionCount returns the number of active sessions (useful for testing)
func (s *Store) GetSessionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}
