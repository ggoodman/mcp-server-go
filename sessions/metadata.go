package sessions

// NOTE: This file introduces the new metadata types for the upcoming session
// refactor. The existing SessionHost interface and session manager still use
// the legacy stateless / revocation + epoch model. Subsequent changes will
// migrate the codebase to rely on these types and will simplify the host
// interface accordingly. Keeping this file isolated allows incremental
// adaptation without breaking current consumers.
//
// Planned migration steps (summarized):
//  1. Introduce these types (this change).
//  2. Replace SessionHost interface to use CreateSession / GetSession /
//     MutateSession / TouchSession / DeleteSession plus per-session KV.
//  3. Rebuild session manager around stored metadata (dropping JWS + epochs).
//  4. Update memory + redis hosts to implement new interface.
//  5. Remove legacy revocation/epoch code paths and types.

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
// MetadataClientInfo is an expanded form of client identity used only in the
// upcoming refactor. It deliberately does not reuse the existing ClientInfo
// type (defined in types.go) to avoid breaking current code while both coexist.
// Once the refactor lands fully these will be unified.
type MetadataClientInfo struct {
	Name       string `json:"name,omitempty"`
	Version    string `json:"version,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
}

// SessionMetadata is the authoritative persisted representation of an MCP
// session. Invalidation and lifetime are handled via stored flags + TTL
// semantics in the host.
//
// Fields marked immutable must not be changed after creation (enforced at the
// manager layer when the refactor lands). Timestamps are wall-clock times in
// UTC. TTL is a sliding window: the host SHOULD expire a session if
// LastAccess + TTL < now (subject to debounce). If MaxLifetime > 0, the host
// MUST also expire the session once CreatedAt + MaxLifetime < now regardless
// of activity.
type SessionMetadata struct {
	MetaVersion     int                `json:"meta_version"`               // For forward migration; starts at 1
	SessionID       string             `json:"session_id"`                 // immutable
	UserID          string             `json:"user_id"`                    // immutable
	Issuer          string             `json:"issuer,omitempty"`           // immutable (empty if not enforced)
	ProtocolVersion string             `json:"protocol_version,omitempty"` // immutable after creation handshake
	Client          MetadataClientInfo `json:"client,omitempty"`           // immutable (can relax later if needed)
	Capabilities    CapabilitySet      `json:"capabilities,omitempty"`     // immutable

	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	LastAccess  time.Time     `json:"last_access"`
	TTL         time.Duration `json:"ttl"`
	MaxLifetime time.Duration `json:"max_lifetime,omitempty"`

	Revoked bool `json:"revoked"`

	// Future reserved (not yet populated): potential binding to originating
	// network fingerprint if we later introduce anti-token-theft measures.
	// BoundIPHash string `json:"bound_ip_hash,omitempty"`
}
