package memory

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/internal/jsonrpc"
)

func TestBroker_PublishSubscribe(t *testing.T) {
	b := New()
	ctx := context.Background()
	namespace := "test-namespace"

	// Test basic publish/subscribe
	msg1 := jsonrpc.Message(`{"jsonrpc":"2.0","method":"test","id":1}`)
	eventID1, err := b.Publish(ctx, namespace, msg1)
	if err != nil {
		t.Fatalf("Failed to publish message: %v", err)
	}

	if eventID1 == "" {
		t.Fatal("Expected non-empty event ID")
	}

	// Subscribe to namespace
	stream, err := b.Subscribe(ctx, namespace, "")
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}
	defer stream.Close()

	// Publish another message
	msg2 := jsonrpc.Message(`{"jsonrpc":"2.0","method":"test2","id":2}`)
	eventID2, err := b.Publish(ctx, namespace, msg2)
	if err != nil {
		t.Fatalf("Failed to publish second message: %v", err)
	}

	// Read first message (should be msg2 since we subscribed after msg1)
	envelope, err := stream.Next(ctx)
	if err != nil {
		t.Fatalf("Failed to read message: %v", err)
	}

	if envelope.ID != eventID2 {
		t.Fatalf("Expected event ID %s, got %s", eventID2, envelope.ID)
	}

	if string(envelope.Data) != string(msg2) {
		t.Fatalf("Expected message %s, got %s", msg2, envelope.Data)
	}
}

func TestBroker_ResumeFromEventID(t *testing.T) {
	b := New()
	ctx := context.Background()
	namespace := "test-resume"

	// Publish some messages
	msg1 := jsonrpc.Message(`{"jsonrpc":"2.0","method":"test1","id":1}`)
	eventID1, err := b.Publish(ctx, namespace, msg1)
	if err != nil {
		t.Fatalf("Failed to publish message 1: %v", err)
	}

	msg2 := jsonrpc.Message(`{"jsonrpc":"2.0","method":"test2","id":2}`)
	eventID2, err := b.Publish(ctx, namespace, msg2)
	if err != nil {
		t.Fatalf("Failed to publish message 2: %v", err)
	}

	msg3 := jsonrpc.Message(`{"jsonrpc":"2.0","method":"test3","id":3}`)
	eventID3, err := b.Publish(ctx, namespace, msg3)
	if err != nil {
		t.Fatalf("Failed to publish message 3: %v", err)
	}

	// Subscribe resuming from eventID1 (should get msg2 and msg3)
	stream, err := b.Subscribe(ctx, namespace, eventID1)
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}
	defer stream.Close()

	// Should receive msg2
	envelope, err := stream.Next(ctx)
	if err != nil {
		t.Fatalf("Failed to read message 2: %v", err)
	}
	if envelope.ID != eventID2 {
		t.Fatalf("Expected event ID %s, got %s", eventID2, envelope.ID)
	}

	// Should receive msg3
	envelope, err = stream.Next(ctx)
	if err != nil {
		t.Fatalf("Failed to read message 3: %v", err)
	}
	if envelope.ID != eventID3 {
		t.Fatalf("Expected event ID %s, got %s", eventID3, envelope.ID)
	}

	// Publish another message and verify it's received
	msg4 := jsonrpc.Message(`{"jsonrpc":"2.0","method":"test4","id":4}`)
	eventID4, err := b.Publish(ctx, namespace, msg4)
	if err != nil {
		t.Fatalf("Failed to publish message 4: %v", err)
	}

	envelope, err = stream.Next(ctx)
	if err != nil {
		t.Fatalf("Failed to read message 4: %v", err)
	}
	if envelope.ID != eventID4 {
		t.Fatalf("Expected event ID %s, got %s", eventID4, envelope.ID)
	}
}

func TestBroker_NamespaceIsolation(t *testing.T) {
	b := New()
	ctx := context.Background()

	namespace1 := "namespace1"
	namespace2 := "namespace2"

	// Subscribe to both namespaces
	stream1, err := b.Subscribe(ctx, namespace1, "")
	if err != nil {
		t.Fatalf("Failed to subscribe to namespace1: %v", err)
	}
	defer stream1.Close()

	stream2, err := b.Subscribe(ctx, namespace2, "")
	if err != nil {
		t.Fatalf("Failed to subscribe to namespace2: %v", err)
	}
	defer stream2.Close()

	// Publish to namespace1
	msg1 := jsonrpc.Message(`{"jsonrpc":"2.0","method":"ns1","id":1}`)
	eventID1, err := b.Publish(ctx, namespace1, msg1)
	if err != nil {
		t.Fatalf("Failed to publish to namespace1: %v", err)
	}

	// Publish to namespace2
	msg2 := jsonrpc.Message(`{"jsonrpc":"2.0","method":"ns2","id":2}`)
	eventID2, err := b.Publish(ctx, namespace2, msg2)
	if err != nil {
		t.Fatalf("Failed to publish to namespace2: %v", err)
	}

	// stream1 should only receive msg1
	envelope, err := stream1.Next(ctx)
	if err != nil {
		t.Fatalf("Failed to read from stream1: %v", err)
	}
	if envelope.ID != eventID1 {
		t.Fatalf("Expected event ID %s from stream1, got %s", eventID1, envelope.ID)
	}

	// stream2 should only receive msg2
	envelope, err = stream2.Next(ctx)
	if err != nil {
		t.Fatalf("Failed to read from stream2: %v", err)
	}
	if envelope.ID != eventID2 {
		t.Fatalf("Expected event ID %s from stream2, got %s", eventID2, envelope.ID)
	}
}

func TestBroker_Cleanup(t *testing.T) {
	b := New()
	ctx := context.Background()
	namespace := "test-cleanup"

	// Publish a message
	msg := jsonrpc.Message(`{"jsonrpc":"2.0","method":"test","id":1}`)
	_, err := b.Publish(ctx, namespace, msg)
	if err != nil {
		t.Fatalf("Failed to publish message: %v", err)
	}

	// Subscribe
	stream, err := b.Subscribe(ctx, namespace, "")
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Cleanup namespace
	err = b.Cleanup(ctx, namespace)
	if err != nil {
		t.Fatalf("Failed to cleanup namespace: %v", err)
	}

	// Stream should be closed
	_, err = stream.Next(ctx)
	if err != io.EOF {
		t.Fatalf("Expected io.EOF after cleanup, got: %v", err)
	}

	// Publishing to namespace after cleanup should work (creates new namespace)
	_, err = b.Publish(ctx, namespace, msg)
	if err != nil {
		t.Fatalf("Expected publishing to work after cleanup, got: %v", err)
	}

	// Subscribing to namespace after cleanup should work (creates new namespace)
	newStream, err := b.Subscribe(ctx, namespace, "")
	if err != nil {
		t.Fatalf("Expected subscribing to work after cleanup, got: %v", err)
	}
	newStream.Close()
}

func TestBroker_ConcurrentSubscribers(t *testing.T) {
	b := New()
	ctx := context.Background()
	namespace := "test-concurrent"

	const numSubscribers = 10
	const numMessages = 100

	// Create multiple subscribers
	var wg sync.WaitGroup
	receivedCounts := make([]int, numSubscribers)

	for i := 0; i < numSubscribers; i++ {
		wg.Add(1)
		go func(subscriberID int) {
			defer wg.Done()

			stream, err := b.Subscribe(ctx, namespace, "")
			if err != nil {
				t.Errorf("Subscriber %d failed to subscribe: %v", subscriberID, err)
				return
			}
			defer stream.Close()

			// Read messages with timeout
			readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			for {
				_, err := stream.Next(readCtx)
				if err != nil {
					if err != context.DeadlineExceeded {
						t.Errorf("Subscriber %d error reading: %v", subscriberID, err)
					}
					return
				}
				receivedCounts[subscriberID]++
				if receivedCounts[subscriberID] >= numMessages {
					return
				}
			}
		}(i)
	}

	// Give subscribers time to set up
	time.Sleep(100 * time.Millisecond)

	// Publish messages
	for i := 0; i < numMessages; i++ {
		msg := jsonrpc.Message(`{"jsonrpc":"2.0","method":"test","id":` + string(rune('0'+i%10)) + `}`)
		_, err := b.Publish(ctx, namespace, msg)
		if err != nil {
			t.Fatalf("Failed to publish message %d: %v", i, err)
		}
	}

	wg.Wait()

	// Verify all subscribers received all messages
	for i, count := range receivedCounts {
		if count != numMessages {
			t.Errorf("Subscriber %d received %d messages, expected %d", i, count, numMessages)
		}
	}
}

func TestBroker_ContextCancellation(t *testing.T) {
	b := New()
	namespace := "test-cancel"

	// Create a context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())

	stream, err := b.Subscribe(ctx, namespace, "")
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}
	defer stream.Close()

	// Cancel context
	cancel()

	// Next should return context cancellation error
	_, err = stream.Next(ctx)
	if err != context.Canceled {
		t.Fatalf("Expected context.Canceled, got: %v", err)
	}
}

func TestBroker_StreamClose(t *testing.T) {
	b := New()
	ctx := context.Background()
	namespace := "test-stream-close"

	stream, err := b.Subscribe(ctx, namespace, "")
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Close the stream
	err = stream.Close()
	if err != nil {
		t.Fatalf("Failed to close stream: %v", err)
	}

	// Next should return EOF
	_, err = stream.Next(ctx)
	if err != io.EOF {
		t.Fatalf("Expected io.EOF after close, got: %v", err)
	}

	// Multiple closes should be safe
	err = stream.Close()
	if err != nil {
		t.Fatalf("Second close failed: %v", err)
	}
}
