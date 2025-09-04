```go
// Broker handles message queuing and delivery concerns
type Broker interface {
    // Publish creates envelope with generated event ID and publishes to namespace
    // Returns the generated event ID for the published message
    Publish(ctx context.Context, namespace string, message jsonrpc.Message) (eventID string, error)

    // Subscribe to namespace messages, resuming from lastEventID if provided
    Subscribe(ctx context.Context, namespace string, lastEventID string) (MessageStream, error)

    // Clean up all resources associated with a namespace
    Cleanup(ctx context.Context, namespace string) error
}

// MessageStream provides ordered message consumption
type MessageStream interface {
    // Next blocks until the next message is available or context is cancelled
    Next(ctx context.Context) (MessageEnvelope, error)

    // Close releases resources associated with this stream
    Close() error
}

type MessageEnvelope struct {
  ID string `json:"id"`
  Data []byte `json:"data"`
}
```
