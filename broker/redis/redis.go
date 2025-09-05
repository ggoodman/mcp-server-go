package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/broker"
	"github.com/ggoodman/mcp-streaming-http-go/internal/jsonrpc"
	"github.com/redis/go-redis/v9"
)

// Broker is a Redis Streams-based implementation of the broker.Broker interface.
// It provides namespace-based message isolation and ordered delivery guarantees
// using Redis Streams for horizontal scalability.
type Broker struct {
	client    redis.UniversalClient
	keyPrefix string
}

// Config contains configuration options for the Redis broker.
type Config struct {
	// Client is the Redis client to use. If nil, a default client will be created.
	Client redis.UniversalClient
	// KeyPrefix is prepended to all Redis keys used by the broker.
	// Defaults to "mcp:broker:" if empty.
	KeyPrefix string
}

// New creates a new Redis-based broker instance.
func New(config Config) *Broker {
	client := config.Client
	if client == nil {
		client = redis.NewClient(&redis.Options{
			Addr: "localhost:6379",
		})
	}

	keyPrefix := config.KeyPrefix
	if keyPrefix == "" {
		keyPrefix = "mcp:broker:"
	}

	return &Broker{
		client:    client,
		keyPrefix: keyPrefix,
	}
}

// Close closes the Redis connection.
func (b *Broker) Close() error {
	return b.client.Close()
}

// Publish creates envelope with generated event ID and publishes to namespace.
// Returns the generated event ID for the published message.
func (b *Broker) Publish(ctx context.Context, namespace string, message jsonrpc.Message) (string, error) {
	data := []byte(message)

	streamKey := b.streamKey(namespace)

	// Use XADD to add message to stream, Redis will generate the ID
	result := b.client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: map[string]any{
			"data": data,
		},
	})

	eventID, err := result.Result()
	if err != nil {
		return "", fmt.Errorf("failed to publish message to stream %s: %w", streamKey, err)
	}

	return eventID, nil
}

// Subscribe to namespace messages, calling handler for each message.
// If lastEventID is empty, subscription starts from the next published message.
// If lastEventID is provided, subscription resumes from the message after that ID.
func (b *Broker) Subscribe(ctx context.Context, namespace string, lastEventID string, handler broker.MessageHandler) error {
	streamKey := b.streamKey(namespace)

	// Determine start position
	startID := "$" // Start from latest message if no lastEventID
	if lastEventID != "" {
		startID = lastEventID
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Read from stream without consumer group (to get all messages for all subscribers)
		streams, err := b.client.XRead(ctx, &redis.XReadArgs{
			Streams: []string{streamKey, startID},
			Count:   1,
			Block:   time.Second, // Block for 1 second, then check context
		}).Result()

		if err != nil {
			if err == redis.Nil {
				// No messages available, continue
				continue
			}
			return fmt.Errorf("failed to read from stream %s: %w", streamKey, err)
		}

		for _, stream := range streams {
			for _, message := range stream.Messages {
				data, ok := message.Values["data"].(string)
				if !ok {
					// Skip malformed message and continue from next
					startID = message.ID
					continue
				}

				envelope := broker.MessageEnvelope{
					ID:   message.ID,
					Data: []byte(data),
				}

				if err := handler(ctx, envelope); err != nil {
					return err
				}

				// Update start position for next read
				startID = message.ID
			}
		}
	}
}

// Cleanup removes all resources associated with a namespace.
func (b *Broker) Cleanup(ctx context.Context, namespace string) error {
	streamKey := b.streamKey(namespace)

	// Delete the stream
	err := b.client.Del(ctx, streamKey).Err()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("failed to cleanup namespace %s: %w", namespace, err)
	}

	return nil
}

func (b *Broker) streamKey(namespace string) string {
	return b.keyPrefix + "stream:" + namespace
}
