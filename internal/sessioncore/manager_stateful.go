package sessioncore

// New stateful session manager skeleton (work-in-progress).
//
// This coexists with the legacy token/epoch-based manager until the refactor
// completes. It is intentionally not wired into handlers yet. Subsequent
// commits will:
//  - Replace the public sessions.SessionHost interface.
//  - Implement host CRUD + KV in memory/redis hosts.
//  - Switch streaminghttp handler to use this manager.
//  - Remove the legacy manager and revocation/epoch code.

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/google/uuid"
)

// MetricsSink allows optional instrumentation without hard dependency.
type MetricsSink interface {
	IncCounter(name string, tags map[string]string)
	ObserveHistogram(name string, value float64, tags map[string]string)
}

// ManagerConfig configures the new stateful session manager.
type ManagerConfig struct {
	DefaultTTL        time.Duration
	MinTTL            time.Duration
	MaxTTL            time.Duration
	TouchDebounce     time.Duration
	DataMaxValueBytes int
	AllowOverwrite    bool // last-write-wins if true; if false future extension
	Metrics           MetricsSink
	Logger            *slog.Logger
}

// applyDefaults populates zero values with conservative defaults.
func (c *ManagerConfig) applyDefaults() {
	if c.DefaultTTL == 0 {
		c.DefaultTTL = 24 * time.Hour
	}
	if c.TouchDebounce == 0 {
		c.TouchDebounce = 5 * time.Second
	}
	if c.DataMaxValueBytes == 0 {
		c.DataMaxValueBytes = 8 * 1024
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// HostFacade is the subset of the future SessionHost interface this manager needs.
// We duplicate here to avoid breaking existing code until the interface is replaced.
type HostFacade interface {
	// Metadata lifecycle (to be added to sessions.SessionHost soon)
	CreateSession(ctx context.Context, meta *sessions.SessionMetadata) error
	GetSession(ctx context.Context, sessionID string) (*sessions.SessionMetadata, error)
	MutateSession(ctx context.Context, sessionID string, fn func(*sessions.SessionMetadata) error) error
	TouchSession(ctx context.Context, sessionID string) error
	DeleteSession(ctx context.Context, sessionID string) error

	// KV storage
	PutSessionData(ctx context.Context, sessionID, key string, value []byte) error
	GetSessionData(ctx context.Context, sessionID, key string) ([]byte, error)
	DeleteSessionData(ctx context.Context, sessionID, key string) error
}

// Manager orchestrates creation, loading, and deletion of sessions backed by
// host-stored metadata. It is safe for concurrent use.
type Manager struct {
	host        HostFacade
	cfg         ManagerConfig
	lastTouchMu sync.Mutex
	lastTouch   map[string]time.Time
}

// NewStatefulManager constructs a stateful manager (temporary name to avoid collision).
func NewStatefulManager(host HostFacade, cfg ManagerConfig) *Manager {
	cfg.applyDefaults()
	return &Manager{host: host, cfg: cfg, lastTouch: make(map[string]time.Time)}
}

// Errors returned by the manager.
var (
	ErrSessionNotFound       = errors.New("session not found")
	ErrSessionUserMismatch   = errors.New("session user mismatch")
	ErrSessionIssuerMismatch = errors.New("session issuer mismatch")
	ErrSessionRevoked        = errors.New("session revoked")
	ErrDataTooLarge          = errors.New("session data value exceeds max bytes")
)

// CreateSession allocates and persists a new session metadata record.
func (m *Manager) CreateSession(ctx context.Context, userID, issuer, proto string, caps sessions.CapabilitySet, client sessions.MetadataClientInfo, ttlOverride, maxLifetime time.Duration) (*StatefulSessionHandle, error) {
	now := time.Now().UTC()
	ttl := ttlOverride
	if ttl <= 0 {
		ttl = m.cfg.DefaultTTL
	}
	if m.cfg.MinTTL > 0 && ttl < m.cfg.MinTTL {
		ttl = m.cfg.MinTTL
	}
	if m.cfg.MaxTTL > 0 && ttl > m.cfg.MaxTTL {
		ttl = m.cfg.MaxTTL
	}
	id := uuid.NewString()
	meta := &sessions.SessionMetadata{
		MetaVersion:     1,
		SessionID:       id,
		UserID:          userID,
		Issuer:          issuer,
		ProtocolVersion: proto,
		Client:          client,
		Capabilities:    caps,
		CreatedAt:       now,
		UpdatedAt:       now,
		LastAccess:      now,
		TTL:             ttl,
		MaxLifetime:     maxLifetime,
	}
	if err := m.host.CreateSession(ctx, meta); err != nil {
		return nil, err
	}
	m.recordMetric("sessions_created", nil)
	return &StatefulSessionHandle{meta: meta, mgr: m}, nil
}

// LoadSession fetches and validates a session for the specified user/issuer.
func (m *Manager) LoadSession(ctx context.Context, sessionID, expectUser, expectIssuer string) (*StatefulSessionHandle, error) {
	meta, err := m.host.GetSession(ctx, sessionID)
	if err != nil {
		return nil, ErrSessionNotFound
	}
	if meta.Revoked {
		return nil, ErrSessionRevoked
	}
	if meta.UserID != expectUser {
		return nil, ErrSessionUserMismatch
	}
	if meta.Issuer != "" && expectIssuer != "" && meta.Issuer != expectIssuer {
		return nil, ErrSessionIssuerMismatch
	}
	now := time.Now().UTC()
	m.maybeTouch(meta.SessionID, now)
	return &StatefulSessionHandle{meta: meta, mgr: m}, nil
}

// DeleteSession deletes the session idempotently.
func (m *Manager) DeleteSession(ctx context.Context, sessionID string) error {
	if err := m.host.DeleteSession(ctx, sessionID); err != nil {
		return err
	}
	m.recordMetric("sessions_deleted", nil)
	return nil
}

// PutData stores a key/value enforcing per-value size.
func (m *Manager) PutData(ctx context.Context, sessionID, key string, value []byte) error {
	if len(value) > m.cfg.DataMaxValueBytes {
		m.recordMetric("session_data_put_rejected", map[string]string{"reason": "too_large"})
		return ErrDataTooLarge
	}
	if err := m.host.PutSessionData(ctx, sessionID, key, value); err != nil {
		m.recordMetric("session_data_put_rejected", map[string]string{"reason": "host"})
		return err
	}
	m.recordMetric("session_data_put", nil)
	return nil
}

func (m *Manager) GetData(ctx context.Context, sessionID, key string) ([]byte, error) {
	v, err := m.host.GetSessionData(ctx, sessionID, key)
	if err == nil {
		m.recordMetric("session_data_get", nil)
	}
	return v, err
}

func (m *Manager) DeleteData(ctx context.Context, sessionID, key string) error {
	if err := m.host.DeleteSessionData(ctx, sessionID, key); err != nil {
		return err
	}
	m.recordMetric("session_data_delete", nil)
	return nil
}

// maybeTouch debounces touch operations.
func (m *Manager) maybeTouch(sessionID string, now time.Time) {
	if m.cfg.TouchDebounce <= 0 {
		_ = m.host.TouchSession(context.Background(), sessionID)
		return
	}
	m.lastTouchMu.Lock()
	last := m.lastTouch[sessionID]
	if !last.IsZero() && now.Sub(last) < m.cfg.TouchDebounce {
		m.lastTouchMu.Unlock()
		m.recordMetric("sessions_touch_debounced", nil)
		return
	}
	m.lastTouch[sessionID] = now
	m.lastTouchMu.Unlock()
	// Fire-and-forget: best-effort
	go func() { _ = m.host.TouchSession(context.Background(), sessionID) }()
}

func (m *Manager) recordMetric(name string, tags map[string]string) {
	if m.cfg.Metrics != nil {
		m.cfg.Metrics.IncCounter(name, tags)
	}
}

// SessionHandle is the internal representation; produces the public session.
type StatefulSessionHandle struct {
	meta *sessions.SessionMetadata
	mgr  *Manager
}

// Internal accessor helpers (avoid exposing meta directly outside package).
func (h *StatefulSessionHandle) ID() string       { return h.meta.SessionID }
func (h *StatefulSessionHandle) Protocol() string { return h.meta.ProtocolVersion }
func (h *StatefulSessionHandle) User() string     { return h.meta.UserID }

// ToSession constructs the public session implementation (to be added later).
func (h *StatefulSessionHandle) ToSession() sessions.Session {
	if h == nil || h.meta == nil {
		return nil
	}
	impl := &statefulSessionImpl{handle: h}
	caps := h.meta.Capabilities

	// We need a backend implementing PublishSession + event pub/sub for capabilities.
	// If the underlying host implements the full sessions.SessionHost, use it; else skip wiring.
	var full sessions.SessionHost
	if fh, ok := h.mgr.host.(sessions.SessionHost); ok {
		full = fh
	}
	if full != nil {
		base := &SimpleSession{id: h.meta.SessionID, backend: full}
		if caps.Sampling {
			impl.sampling = &samplingCapabilityImpl{sess: base}
		}
		if caps.Roots {
			impl.roots = &rootsCapabilityImpl{sess: base, supportsListChanged: caps.RootsListChanged}
		}
		if caps.Elicitation {
			impl.elicitation = &elicitationCapabilityImpl{sess: base}
		}
	}
	return impl
}

// statefulSessionImpl is the concrete public session for the stateful manager.
type statefulSessionImpl struct {
	handle      *StatefulSessionHandle
	sampling    sessions.SamplingCapability
	roots       sessions.RootsCapability
	elicitation sessions.ElicitationCapability
}

func (s *statefulSessionImpl) SessionID() string       { return s.handle.meta.SessionID }
func (s *statefulSessionImpl) UserID() string          { return s.handle.meta.UserID }
func (s *statefulSessionImpl) ProtocolVersion() string { return s.handle.meta.ProtocolVersion }
func (s *statefulSessionImpl) GetSamplingCapability() (sessions.SamplingCapability, bool) {
	if s.sampling == nil {
		return nil, false
	}
	return s.sampling, true
}
func (s *statefulSessionImpl) GetRootsCapability() (sessions.RootsCapability, bool) {
	if s.roots == nil {
		return nil, false
	}
	return s.roots, true
}
func (s *statefulSessionImpl) GetElicitationCapability() (sessions.ElicitationCapability, bool) {
	if s.elicitation == nil {
		return nil, false
	}
	return s.elicitation, true
}

// KV data access (SessionData interface)
func (s *statefulSessionImpl) PutData(ctx context.Context, key string, value []byte) error {
	return s.handle.mgr.PutData(ctx, s.handle.meta.SessionID, key, value)
}
func (s *statefulSessionImpl) GetData(ctx context.Context, key string) ([]byte, error) {
	return s.handle.mgr.GetData(ctx, s.handle.meta.SessionID, key)
}
func (s *statefulSessionImpl) DeleteData(ctx context.Context, key string) error {
	return s.handle.mgr.DeleteData(ctx, s.handle.meta.SessionID, key)
}
