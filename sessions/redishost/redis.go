package redishost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/redis/go-redis/v9"
)

// Host is a Redis-backed implementation of sessions.SessionHost using
// Redis Streams for ordered messaging and for server-internal events.
//
// Rationale:
//   - Streams give us durable, ordered event delivery semantics and a simple
//     polling API (XREAD) without managing consumer-group state for events.
//   - We intentionally start new subscribers at "$" (the "now" snapshot) to only
//     deliver events published after SubscribeEvents is called. Tests introduce a
//     small handshake barrier to avoid racy first publishes; production does not
//     require microsecond fidelity.
//
// Note: We trim streams approximately to cap growth, and we don't replay events
// to new subscribers (late subscribers only see future events).
type Host struct {
	client    *redis.Client
	keyPrefix string
	// retention / recovery settings
	streamMaxLen  int64         // approximate max stream length per session (0 = unbounded)
	claimMinIdle  time.Duration // pending idle >= this become eligible for auto-claim
	claimInterval time.Duration // how often to attempt auto-claim
}

// Option configures the Redis-backed host.
type Option func(*Host)

// WithKeyPrefix sets the key prefix for all Redis keys used by the host.
func WithKeyPrefix(prefix string) Option {
	return func(h *Host) {
		if prefix != "" {
			h.keyPrefix = prefix
		}
	}
}

// WithStreamMaxLen sets an approximate upper bound for each session stream length.
// When > 0, XADD uses MAXLEN ~ streamMaxLen to cap memory growth.
func WithStreamMaxLen(n int64) Option { return func(h *Host) { h.streamMaxLen = n } }

// WithClaimSettings configures auto-claim behavior for orphaned pending messages.
// minIdle is the minimum idle time before a pending message becomes eligible;
// interval controls how frequently auto-claim is attempted.
func WithClaimSettings(minIdle, interval time.Duration) Option {
	return func(h *Host) {
		if minIdle > 0 {
			h.claimMinIdle = minIdle
		}
		if interval > 0 {
			h.claimInterval = interval
		}
	}
}

// New constructs a Host connecting to the provided Redis address (e.g. "localhost:6379").
// Options can configure behavior such as key prefix. The connection is verified via PING.
func New(redisAddr string, opts ...Option) (*Host, error) {
	options, err := redis.ParseURL(redisAddr)
	if err != nil {
		return nil, fmt.Errorf("redis parse url: %w", err)
	}
	cl := redis.NewClient(options)
	if err := cl.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	h := &Host{
		client:        cl,
		keyPrefix:     "mcp:sessions:",
		streamMaxLen:  10000,
		claimMinIdle:  5 * time.Second,
		claimInterval: 1 * time.Second,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h, nil
}

// Close closes the underlying Redis client.
func (h *Host) Close() error { return h.client.Close() }

// --- Key helpers ---

func (h *Host) streamKey(sessionID string) string { return h.keyPrefix + "stream:" + sessionID }
func (h *Host) metaKey(sessionID string) string   { return h.keyPrefix + "meta:" + sessionID }
func (h *Host) groupName() string                 { return h.keyPrefix + "cg" }
func (h *Host) dataKey(sessionID, key string) string {
	return h.keyPrefix + "data:" + sessionID + ":" + key
}
func (h *Host) dataScanPattern(sessionID string) string {
	return h.keyPrefix + "data:" + sessionID + ":*"
}
func (h *Host) eventStreamKey(topic string) string { return h.keyPrefix + "evtstream:" + topic }

// --- Messaging via Redis Streams ---

func (h *Host) PublishSession(ctx context.Context, sessionID string, data []byte) (string, error) {
	xargs := &redis.XAddArgs{Stream: h.streamKey(sessionID), Values: map[string]any{"d": data}}
	if h.streamMaxLen > 0 {
		xargs.Approx = true
		xargs.MaxLen = h.streamMaxLen
	}
	id, err := h.client.XAdd(ctx, xargs).Result()
	if err != nil {
		return "", err
	}
	// Best-effort: align stream expiry with session metadata TTL
	if ttl, terr := h.client.TTL(ctx, h.metaKey(sessionID)).Result(); terr == nil && ttl > 0 {
		_ = h.client.Expire(ctx, h.streamKey(sessionID), ttl).Err()
	}
	return id, nil
}

func (h *Host) SubscribeSession(ctx context.Context, sessionID string, lastEventID string, handler sessions.MessageHandlerFunction) error {
	key := h.streamKey(sessionID)
	group := h.groupName()

	// Create consumer group idempotently. Use lastEventID as the group's starting point if provided,
	// otherwise use "$" to only see new entries after group creation. If the group already exists we do not
	// adjust its start position.
	startID := "$"
	if lastEventID != "" {
		startID = lastEventID
	}
	if err := h.client.XGroupCreateMkStream(ctx, key, group, startID).Err(); err != nil {
		// go-redis returns BUSYGROUP string when group exists
		if !strings.Contains(err.Error(), "BUSYGROUP") {
			return err
		}
	}

	// Unique consumer per SubscribeSession call
	consumer := fmt.Sprintf("c:%s:%d:%04x", sessionID, time.Now().UnixNano(), rand.Intn(0xffff))

	decode := func(v any) []byte {
		switch t := v.(type) {
		case string:
			return []byte(t)
		case []byte:
			return t
		default:
			return []byte(fmt.Sprintf("%v", t))
		}
	}

	// Helper to process a batch of messages and ack on success.
	process := func(msgs []redis.XMessage) error {
		for _, m := range msgs {
			payload := decode(m.Values["d"])
			if err := handler(ctx, m.ID, payload); err != nil {
				// Do not ACK on handler error; allow redelivery by another subscriber.
				return err
			}
			// Best-effort ack; ignore ack error (will cause redelivery later).
			_ = h.client.XAck(ctx, key, group, m.ID).Err()
		}
		return nil
	}

	// Main loop: consume new entries; periodically auto-claim orphaned pending entries
	lastClaim := time.Now()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if h.claimInterval > 0 && h.claimMinIdle > 0 && time.Since(lastClaim) >= h.claimInterval {
			lastClaim = time.Now()
			// Iterate through claim batches until no more eligible messages
			start := "0-0"
			for {
				msgs, next, err := h.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
					Stream:   key,
					Group:    group,
					Consumer: consumer,
					MinIdle:  h.claimMinIdle,
					Start:    start,
					Count:    16,
				}).Result()
				if err != nil {
					// ignore claim errors; try again later
					break
				}
				if len(msgs) == 0 {
					break
				}
				if err := process(msgs); err != nil {
					return err
				}
				start = next
			}
		}

		res, err := h.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{key, ">"},
			Count:    1,
			Block:    500 * time.Millisecond,
			NoAck:    false,
		}).Result()
		if err != nil {
			if err == redis.Nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				continue
			}
			return err
		}
		if len(res) == 0 || len(res[0].Messages) == 0 {
			continue
		}
		if err := process(res[0].Messages); err != nil {
			return err
		}
	}
}

// DeleteSession removes all artifacts (idempotent best-effort).
func (h *Host) DeleteSession(ctx context.Context, sessionID string) error {
	c := context.WithoutCancel(ctx)
	// delete metadata
	_, _ = h.client.Del(c, h.metaKey(sessionID)).Result()
	// delete stream
	_, _ = h.client.Del(c, h.streamKey(sessionID)).Result()
	// delete kv entries
	_ = h.deleteByPattern(c, h.dataScanPattern(sessionID))
	return nil
}

// Metadata CRUD (stored as JSON blob for simplicity; could be hash later)
func (h *Host) CreateSession(ctx context.Context, meta *sessions.SessionMetadata) error {
	key := h.metaKey(meta.SessionID)
	// NX to avoid overwrite
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	ok, err := h.client.SetNX(ctx, key, b, meta.TTL).Result()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("session exists")
	}
	return nil
}

func (h *Host) GetSession(ctx context.Context, sessionID string) (*sessions.SessionMetadata, error) {
	val, err := h.client.Get(ctx, h.metaKey(sessionID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, errors.New("not found")
		}
		return nil, err
	}
	var m sessions.SessionMetadata
	if err := json.Unmarshal(val, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (h *Host) MutateSession(ctx context.Context, sessionID string, fn func(*sessions.SessionMetadata) error) error {
	key := h.metaKey(sessionID)
	// Use WATCH to implement CAS loop
	for i := 0; i < 5; i++ { // retry bound
		err := h.client.Watch(ctx, func(tx *redis.Tx) error {
			val, err := tx.Get(ctx, key).Bytes()
			if err != nil {
				if err == redis.Nil {
					return errors.New("not found")
				}
				return err
			}
			var m sessions.SessionMetadata
			if err := json.Unmarshal(val, &m); err != nil {
				return err
			}
			if err := fn(&m); err != nil {
				return err
			}
			m.UpdatedAt = time.Now().UTC()
			b, err := json.Marshal(&m)
			if err != nil {
				return err
			}
			// Determine refreshed sliding TTL. We intentionally RESET the key TTL to m.TTL
			// (clamped by remaining MaxLifetime) to implement a proper sliding window.
			// Previous logic preserved the remaining TTL which caused sessions opened
			// after a short handshake TTL to expire prematurely.
			remaining := m.TTL
			if m.MaxLifetime > 0 {
				// Clamp refreshed TTL so we never extend past absolute max lifetime.
				elapsed := time.Since(m.CreatedAt)
				if elapsed >= m.MaxLifetime {
					// Session exceeded absolute lifetime: treat as not found to upstream.
					return errors.New("not found")
				}
				left := m.MaxLifetime - elapsed
				if left < remaining {
					remaining = left
				}
			}
			p := tx.TxPipeline()
			p.Set(ctx, key, b, remaining)
			// Align stream expiry with refreshed TTL (best-effort)
			p.Expire(ctx, h.streamKey(m.SessionID), remaining)
			_, err = p.Exec(ctx)
			return err
		}, key)
		if err == redis.TxFailedErr {
			continue
		}
		return err
	}
	return errors.New("mutation retries exceeded")
}

func (h *Host) TouchSession(ctx context.Context, sessionID string) error {
	key := h.metaKey(sessionID)
	// Refresh sliding TTL to full duration (clamped by MaxLifetime) on activity.
	return h.client.Watch(ctx, func(tx *redis.Tx) error {
		val, err := tx.Get(ctx, key).Bytes()
		if err != nil {
			return err
		}
		var m sessions.SessionMetadata
		if err := json.Unmarshal(val, &m); err != nil {
			return err
		}
		m.LastAccess = time.Now().UTC()
		m.UpdatedAt = m.LastAccess
		// Compute refreshed TTL.
		newTTL := m.TTL
		if m.MaxLifetime > 0 {
			elapsed := time.Since(m.CreatedAt)
			if elapsed >= m.MaxLifetime {
				return errors.New("not found")
			}
			remain := m.MaxLifetime - elapsed
			if remain < newTTL {
				newTTL = remain
			}
		}
		b, err := json.Marshal(&m)
		if err != nil {
			return err
		}
		p := tx.TxPipeline()
		p.Set(ctx, key, b, newTTL)
		p.Expire(ctx, h.streamKey(sessionID), newTTL)
		_, err = p.Exec(ctx)
		return err
	}, key)
}

// KV storage
func (h *Host) PutSessionData(ctx context.Context, sessionID, key string, value []byte) error {
	rkey := h.dataKey(sessionID, key)
	// Align TTL with metadata object (best-effort) by reading meta TTL.
	ttl, _ := h.client.TTL(ctx, h.metaKey(sessionID)).Result()
	if ttl < 0 {
		ttl = time.Hour
	} // fallback default
	return h.client.Set(ctx, rkey, value, ttl).Err()
}
func (h *Host) GetSessionData(ctx context.Context, sessionID, key string) ([]byte, bool, error) {
	v, err := h.client.Get(ctx, h.dataKey(sessionID, key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, false, nil
		}
		return nil, false, err
	}
	return v, true, nil
}
func (h *Host) DeleteSessionData(ctx context.Context, sessionID, key string) error {
	_, err := h.client.Del(ctx, h.dataKey(sessionID, key)).Result()
	return err
}

// Interface compliance
var _ sessions.SessionHost = (*Host)(nil)

// --- Helpers ---

func (h *Host) deleteByPattern(ctx context.Context, pattern string) error {
	var cursor uint64
	for {
		keys, cur, err := h.client.Scan(ctx, cursor, pattern, 50).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			_, _ = h.client.Del(ctx, keys...).Result()
		}
		if cur == 0 {
			return nil
		}
		cursor = cur
	}
}

// --- Server-internal event pub/sub using Redis Pub/Sub ---

func (h *Host) PublishEvent(ctx context.Context, topic string, payload []byte) error {
	key := h.eventStreamKey(topic)
	// minimal retention: rely on stream trimming (approximate) to cap size; we don't replay anyway.
	// Use MAXLEN ~ 1000 (arbitrary) to avoid unbounded growth; config tweak later if needed.
	// If trimming fails, still deliver event.
	_, err := h.client.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: map[string]any{"d": payload}, Approx: true, MaxLen: 1000}).Result()
	return err
}

func (h *Host) SubscribeEvents(ctx context.Context, topic string, handler sessions.EventHandlerFunction) error {
	key := h.eventStreamKey(topic)
	// First read uses the special "$" ID to mean "only entries added after this call".
	// Subsequent reads use the last delivered entry ID for strict ordering.
	lastID := "$"
	ready := make(chan struct{})
	go func(start chan struct{}) {
		first := true
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if first {
				// Signal armed just before issuing the first blocking read
				first = false
				close(start)
				start = nil // drop reference to allow GC
			}
			res, err := h.client.XRead(ctx, &redis.XReadArgs{Streams: []string{key, lastID}, Block: 500 * time.Millisecond}).Result()
			if err != nil {
				if err == redis.Nil || errors.Is(err, context.DeadlineExceeded) {
					continue
				}
				select {
				case <-time.After(100 * time.Millisecond):
				case <-ctx.Done():
					return
				}
				continue
			}
			if len(res) == 0 || len(res[0].Messages) == 0 {
				continue
			}
			for _, m := range res[0].Messages {
				lastID = m.ID
				var payload []byte
				switch v := m.Values["d"].(type) {
				case string:
					payload = []byte(v)
				case []byte:
					payload = v
				default:
					payload = []byte(fmt.Sprintf("%v", v))
				}
				if err := handler(ctx, payload); err != nil {
					return
				}
			}
		}
	}(ready)
	// Wait until the reader goroutine has armed its first blocking read,
	// but don't block forever if the context is canceled early.
	select {
	case <-ready:
		// armed
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
