package broker

import (
	"context"

	"github.com/ggoodman/mcp-streaming-http-go/internal/jsonrpc"
)

// Broker handles message queuing and delivery concerns for horizontally scalable
// MCP streaming HTTP transport. It provides namespace-based message isolation
// and ordered delivery guarantees within each namespace.
type Broker interface {
	// Publish creates envelope with generated event ID and publishes to namespace.
	// Returns the generated event ID for the published message.
	// The message will be JSON-marshaled and stored as the envelope's data.
	Publish(ctx context.Context, namespace string, message jsonrpc.Message) (eventID string, err error)

	// Subscribe to namespace messages, resuming from lastEventID if provided.
	// If lastEventID is empty, subscription starts from the next published message.
	// If lastEventID is provided, subscription resumes from the message after that ID.
	Subscribe(ctx context.Context, namespace string, lastEventID string) (MessageStream, error)

	// Cleanup removes all resources associated with a namespace.
	// This includes all stored messages and active subscriptions.
	Cleanup(ctx context.Context, namespace string) error
}

// MessageStream provides ordered message consumption within a namespace.
// Streams are safe for concurrent use by a single consumer.
type MessageStream interface {
	// Next blocks until the next message is available or context is cancelled.
	// Returns io.EOF when the stream is closed and no more messages are available.
	Next(ctx context.Context) (MessageEnvelope, error)

	// Close releases resources associated with this stream.
	// After Close is called, Next will return an error.
	Close() error
}

// MessageEnvelope wraps a message with metadata for ordered delivery.
type MessageEnvelope struct {
	// ID is a unique, monotonically increasing identifier for this message within the namespace
	ID string `json:"id"`
	// Data is the JSON-serialized message content
	Data []byte `json:"data"`
}
