package sessions

// Session metadata types used to persist and manage session lifecycle and
// negotiated capability surface.

import "time"

// CapabilitySet captures the immutable capability surface negotiated at
// session creation. Booleans keep it cheap to serialize, compare, and extend.
type CapabilitySet struct {
	Roots            bool `json:"roots,omitempty"`
	RootsListChanged bool `json:"roots_list_changed,omitempty"`
	Sampling         bool `json:"sampling,omitempty"`
	Elicitation      bool `json:"elicitation,omitempty"`
}

// ClientInfo records optional client identity details supplied at
// initialization for observability / logging. All fields are optional.
// MetadataClientInfo is an expanded form of client identity separate from the
// lightweight ClientInfo used at initialization.
type MetadataClientInfo struct {
	Name       string `json:"name,omitempty"`
	Version    string `json:"version,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
}

// SessionState represents the lifecycle phase of a session.
type SessionState string

const (
	// SessionStatePending indicates the server has responded to initialize but
	// has not yet received the client's notifications/initialized. While pending,
	// the session must not accept requests.
	SessionStatePending SessionState = "pending"
	// SessionStateOpen indicates the client has completed initialization and the
	// session is fully operational.
	SessionStateOpen SessionState = "open"
)

// SessionMetadata is the authoritative persisted representation of an MCP
// session. Invalidation and lifetime are handled via stored flags + TTL
// semantics in the host.
//
// Fields marked immutable must not be changed after creation. Timestamps are
// wall-clock times in UTC. TTL is a sliding window: the host SHOULD expire a session if
// LastAccess + TTL < now (subject to debounce). If MaxLifetime > 0, the host
// MUST also expire the session once CreatedAt + MaxLifetime < now regardless
// of activity.
type SessionMetadata struct {
	MetaVersion int    `json:"meta_version"`     // For forward migration; starts at 1
	SessionID   string `json:"session_id"`       // immutable
	UserID      string `json:"user_id"`          // immutable
	Issuer      string `json:"issuer,omitempty"` // immutable (empty if not enforced)

	// ClientProtocolVersion is the protocol version the client advertised in
	// initialize.
	ClientProtocolVersion string `json:"client_protocol_version,omitempty"` // immutable after creation handshake
	// ServerProtocolVersion is the protocol version the server selected for the
	// session (and returned in initialize).
	ServerProtocolVersion string             `json:"server_protocol_version,omitempty"` // immutable after creation handshake
	Client                MetadataClientInfo `json:"client,omitempty"`                  // immutable (can relax later if needed)
	Capabilities          CapabilitySet      `json:"capabilities,omitempty"`            // immutable
	// State tracks whether the session is pending or open. Missing state should
	// be interpreted as open for backward compatibility.
	State SessionState `json:"state,omitempty"`

	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	LastAccess  time.Time     `json:"last_access"`
	TTL         time.Duration `json:"ttl"`
	MaxLifetime time.Duration `json:"max_lifetime,omitempty"`
	// OpenedAt records when the session transitioned to open.
	OpenedAt time.Time `json:"opened_at,omitempty"`

	Revoked bool `json:"revoked"`

	// Future reserved (not yet populated): potential binding to originating
	// network fingerprint if we later introduce anti-token-theft measures.
	// BoundIPHash string `json:"bound_ip_hash,omitempty"`
}
