package redishost

import (
	"context"
	"fmt"
	"time"

	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/joeshaw/envdecode"
	"github.com/redis/go-redis/v9"
)

// Config for Redis-backed SessionHost. Defaults can be loaded via envdecode.
// See struct tags for environment variable names and defaults.
type Config struct {
	// RedisAddr like "localhost:6379". ENV: REDIS_ADDR
	RedisAddr string `env:"REDIS_ADDR,default=localhost:6379"`
	// KeyPrefix for all keys. ENV: SESSIONS_KEY_PREFIX
	KeyPrefix string `env:"SESSIONS_KEY_PREFIX,default=mcp:sessions:"`
}

// Host is a Redis-backed implementation of sessions.SessionHost using
// Redis Streams for ordered messaging and Pub/Sub for events.
type Host struct {
	client    *redis.Client
	keyPrefix string
}

// New constructs a Host from the provided Config and verifies connectivity.
func New(cfg Config) (*Host, error) {
	addr := cfg.RedisAddr
	if addr == "" {
		// Allow default via envdecode-style tag fallback for external consumers
		addr = "localhost:6379"
	}
	cl := redis.NewClient(&redis.Options{Addr: addr})
	if err := cl.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	prefix := cfg.KeyPrefix
	if prefix == "" {
		prefix = "mcp:sessions:"
	}
	return &Host{client: cl, keyPrefix: prefix}, nil
}

// NewFromEnv builds a Host using envdecode to populate Config.
func NewFromEnv() (*Host, error) {
	var cfg Config
	// Use envdecode; defaults are provided via struct tags.
	_ = envdecode.Decode(&cfg)
	return New(cfg)
}

// Close closes the underlying Redis client.
func (h *Host) Close() error { return h.client.Close() }

// --- Key helpers ---

func (h *Host) streamKey(sessionID string) string  { return h.keyPrefix + "stream:" + sessionID }
func (h *Host) revokedKey(sessionID string) string { return h.keyPrefix + "revoked:" + sessionID }
func (h *Host) epochKey(scope sessions.RevocationScope) string {
	return h.keyPrefix + "epoch:" + scope.UserID + "|" + scope.ClientID + "|" + scope.TenantID
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

func (h *Host) CleanupSession(ctx context.Context, sessionID string) error {
	// Best-effort delete keys related to this session
	c := context.WithoutCancel(ctx)
	_, _ = h.client.Del(c, h.streamKey(sessionID)).Result()
	// Best-effort: delete any pending awaits/replies for this session
	// Note: SCAN is used to avoid blocking; ignore errors.
	patA := h.keyPrefix + "await:" + sessionID + ":*"
	patR := h.keyPrefix + "reply:" + sessionID + ":*"
	_ = h.deleteByPattern(c, patA)
	_ = h.deleteByPattern(c, patR)
	// Do NOT delete the per-session revocation marker here; it's required to
	// prevent token re-use after DeleteSession. It will expire via TTL.
	return nil
}

// --- Revocation ---

func (h *Host) AddRevocation(ctx context.Context, sessionID string, ttl time.Duration) error {
	key := h.revokedKey(sessionID)
	if ttl <= 0 {
		ttl = time.Hour
	}
	c := context.WithoutCancel(ctx)
	if err := h.client.Set(c, key, "1", ttl).Err(); err != nil {
		return err
	}
	return nil
}

func (h *Host) IsRevoked(ctx context.Context, sessionID string) (bool, error) {
	key := h.revokedKey(sessionID)
	n, err := h.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (h *Host) BumpEpoch(ctx context.Context, scope sessions.RevocationScope) (int64, error) {
	c := context.WithoutCancel(ctx)
	n, err := h.client.Incr(c, h.epochKey(scope)).Result()
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (h *Host) GetEpoch(ctx context.Context, scope sessions.RevocationScope) (int64, error) {
	cmd := h.client.Get(ctx, h.epochKey(scope))
	if err := cmd.Err(); err != nil {
		if err == redis.Nil {
			return 0, nil
		}
		return 0, err
	}
	n, err := cmd.Int64()
	if err != nil {
		return 0, err
	}
	return n, nil
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
		defer sub.Close()
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
