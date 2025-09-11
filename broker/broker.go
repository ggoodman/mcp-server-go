package broker

import (
	"context"
)

// MessageHandler is called for each message received during subscription.
// Return an error to stop the subscription.
type MessageHandler func(ctx context.Context, envelope MessageEnvelope) error

// Broker handles message queuing and delivery concerns for horizontally scalable
// MCP streaming HTTP transport. It provides namespace-based message isolation
// and ordered delivery guarantees within each namespace.
type Broker interface {
	// Publish creates envelope with generated event ID and publishes to namespace.
	// Returns the generated event ID for the published message.
	// The message will be JSON-marshaled and stored as the envelope's data.
	Publish(ctx context.Context, namespace string, message []byte) (eventID string, err error)

	// Subscribe to namespace messages, calling handler for each message.
	// If lastEventID is empty, subscription starts from the next published message.
	// If lastEventID is provided, subscription resumes from the message after that ID.
	// Blocks until context is cancelled or handler returns an error.
	Subscribe(ctx context.Context, namespace string, lastEventID string, handler MessageHandler) error

	// Cleanup removes all resources associated with a namespace.
	// This includes all stored messages and active subscriptions.
	Cleanup(ctx context.Context, namespace string) error
}

// MessageEnvelope wraps a message with metadata for ordered delivery.
type MessageEnvelope struct {
	// ID is a unique, monotonically increasing identifier for this message within the namespace
	ID string
	// Data is the JSON-serialized message content
	Data []byte
}
