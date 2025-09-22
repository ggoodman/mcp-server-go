package redishost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/redis/go-redis/v9"
)

// Host is a Redis-backed implementation of sessions.SessionHost using
// Redis Streams for ordered messaging and Pub/Sub for events.
type Host struct {
	client    *redis.Client
	keyPrefix string
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

// New constructs a Host connecting to the provided Redis address (e.g. "localhost:6379").
// Options can configure behavior such as key prefix. The connection is verified via PING.
func New(redisAddr string, opts ...Option) (*Host, error) {
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	cl := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := cl.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	h := &Host{client: cl, keyPrefix: "mcp:sessions:"}
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
func (h *Host) dataKey(sessionID, key string) string {
	return h.keyPrefix + "data:" + sessionID + ":" + key
}
func (h *Host) dataScanPattern(sessionID string) string {
	return h.keyPrefix + "data:" + sessionID + ":*"
}
func (h *Host) eventChannel(sessionID, topic string) string {
	return h.keyPrefix + "evt:" + sessionID + ":" + topic
}

// --- Messaging via Redis Streams ---

func (h *Host) PublishSession(ctx context.Context, sessionID string, data []byte) (string, error) {
	id, err := h.client.XAdd(ctx, &redis.XAddArgs{Stream: h.streamKey(sessionID), Values: map[string]interface{}{"d": data}}).Result()
	if err != nil {
		return "", err
	}
	return id, nil
}

func (h *Host) SubscribeSession(ctx context.Context, sessionID string, lastEventID string, handler sessions.MessageHandlerFunction) error {
	key := h.streamKey(sessionID)
	start := lastEventID
	if start == "" {
		start = "$"
	} // start from next message

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		res, err := h.client.XRead(ctx, &redis.XReadArgs{Streams: []string{key, start}, Count: 1, Block: 500 * time.Millisecond}).Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			return err
		}
		if len(res) == 0 || len(res[0].Messages) == 0 {
			continue
		}
		for _, m := range res[0].Messages {
			start = m.ID
			// Robust payload decoding: accept string or []byte
			var payload []byte
			switch v := m.Values["d"].(type) {
			case string:
				payload = []byte(v)
			case []byte:
				payload = v
			default:
				// Fallback: best-effort formatting
				payload = []byte(fmt.Sprintf("%v", v))
			}
			if err := handler(ctx, m.ID, payload); err != nil {
				return err
			}
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
			// pipeline set preserving TTL time remaining
			p := tx.TxPipeline()
			// fetch remaining TTL
			ttl, err := tx.TTL(ctx, key).Result()
			if err != nil {
				return err
			}
			if ttl <= 0 {
				ttl = m.TTL
			} // fallback
			p.Set(ctx, key, b, ttl)
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
	// Extend TTL by fetching existing metadata and re-setting value with new TTL (sliding window)
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
		b, err := json.Marshal(&m)
		if err != nil {
			return err
		}
		// Preserve remaining TTL or use m.TTL if no TTL
		ttl, err := tx.TTL(ctx, key).Result()
		if err != nil {
			return err
		}
		if ttl <= 0 {
			ttl = m.TTL
		}
		p := tx.TxPipeline()
		p.Set(ctx, key, b, ttl)
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
func (h *Host) GetSessionData(ctx context.Context, sessionID, key string) ([]byte, error) {
	v, err := h.client.Get(ctx, h.dataKey(sessionID, key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, errors.New("not found")
		}
		return nil, err
	}
	return v, nil
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

func (h *Host) PublishEvent(ctx context.Context, sessionID, topic string, payload []byte) error {
	ch := h.eventChannel(sessionID, topic)
	// PUBLISH returns number of subscribers; we ignore it for best-effort delivery
	return h.client.Publish(ctx, ch, payload).Err()
}

func (h *Host) SubscribeEvents(ctx context.Context, sessionID, topic string, handler sessions.EventHandlerFunction) (func(), error) {
	ch := h.eventChannel(sessionID, topic)
	sub := h.client.Subscribe(ctx, ch)
	// Ensure subscription is active
	if _, err := sub.Receive(ctx); err != nil {
		_ = sub.Close()
		return nil, err
	}
	// Consume messages
	go func() {
		defer func() {
			_ = sub.Close()
		}()
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				// Best-effort handler; ignore errors to keep subscription alive
				_ = handler(ctx, []byte(msg.Payload))
			}
		}
	}()
	unsubscribe := func() { _ = sub.Close() }
	return unsubscribe, nil
}
