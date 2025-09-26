// Package redishost implements sessions.SessionHost using Redis primitives
// (Streams + simple keys) to support horizontally scalable MCP deployments.
// It provides ordered per-session message streams, internal coordination
// topics, metadata persistence with TTL / max lifetime enforcement, and small
// per-session key/value storage.
//
// Design Notes
//   - Session streams: XADD + XREAD consumer-group free polling; at-least-once
//   - Internal events: topic streams started at "$" (only future events)
//   - Metadata: JSON blob stored at key; MutateSession uses optimistic read/modify/write
//   - Trimming: approximate MAXLEN to bound unbounded growth (configurable)
//   - Auto-claim (future): plumbing present via WithClaimSettings for recovering orphaned deliveries
//
// Trade-offs
//
//	Pros: durability, multi-process coordination, simple operational model
//	Cons: eventual trimming, at-least-once semantics require idempotent handlers
//
// Example:
//
//	host, _ := redishost.New("localhost:6379", redishost.WithKeyPrefix("mcp:"))
//	defer host.Close()
//
// Use memoryhost for ephemeral development; use redishost where scale-out or
// restart persistence is required.
package redishost
