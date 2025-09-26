// Package memoryhost provides an in-memory sessions.SessionHost implementation
// suitable for tests, development, and single-process servers. All state is
// ephemeral and discarded on process exit. It implements ordered per-session
// message streaming, internal topic fan-out, metadata persistence, and small
// per-session key/value storage using Go data structures guarded by mutexes.
//
// Characteristics
//
//	Durability        : none (RAM only)
//	Horizontal scale  : no (process local)
//	Ordering          : monotonic decimal IDs per session stream
//	Event delivery    : at-least-once best-effort
//	Concurrency       : safe (RWMutex + per-stream coordination)
//
// Example:
//
//	host := memoryhost.New()
//	// transport wires this host into streaminghttp.New(...)
//
// For production multi-node deployments prefer a durable host like redishost.
package memoryhost
