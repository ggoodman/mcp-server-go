package brokertest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ggoodman/mcp-streaming-http-go/broker"
	"github.com/ggoodman/mcp-streaming-http-go/internal/jsonrpc"
)

// BrokerFactory is a function that creates a new broker instance for testing.
type BrokerFactory func(t *testing.T) broker.Broker

// RunBrokerTests runs the complete broker test suite against the provided factory.
func RunBrokerTests(t *testing.T, factory BrokerFactory) {
	t.Run("PublishAndSubscribeFromBeginning", func(t *testing.T) {
		testPublishAndSubscribeFromBeginning(t, factory)
	})
	t.Run("PublishAndSubscribeFromLastEventID", func(t *testing.T) {
		testPublishAndSubscribeFromLastEventID(t, factory)
	})
	t.Run("MultipleSubscribersToSameNamespace", func(t *testing.T) {
		testMultipleSubscribersToSameNamespace(t, factory)
	})
	t.Run("NamespaceIsolation", func(t *testing.T) {
		testNamespaceIsolation(t, factory)
	})
	t.Run("SubscriptionContextCancellation", func(t *testing.T) {
		testSubscriptionContextCancellation(t, factory)
	})
	t.Run("HandlerErrorStopsSubscription", func(t *testing.T) {
		testHandlerErrorStopsSubscription(t, factory)
	})
	t.Run("Cleanup", func(t *testing.T) {
		testCleanup(t, factory)
	})
	t.Run("ResumeFromNonExistentEventID", func(t *testing.T) {
		testResumeFromNonExistentEventID(t, factory)
	})
}

func testPublishAndSubscribeFromBeginning(t *testing.T, factory BrokerFactory) {
	b := factory(t)
	defer cleanupBroker(t, b)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	namespace := "test-namespace"

	// Create a test message
	req := &jsonrpc.Request{
		JSONRPCVersion: "2.0",
		Method:         "test/method",
		ID:             jsonrpc.NewRequestID(1),
	}
	reqBytes, _ := json.Marshal(req)
	message := jsonrpc.Message(reqBytes)

	var receivedMessages []broker.MessageEnvelope
	var mu sync.Mutex

	// Start subscription (should receive messages published after subscription starts)
	subscriptionDone := make(chan error, 1)
	go func() {
		err := b.Subscribe(ctx, namespace, "", func(ctx context.Context, envelope broker.MessageEnvelope) error {
			mu.Lock()
			receivedMessages = append(receivedMessages, envelope)
			mu.Unlock()

			// Cancel after receiving one message
			if len(receivedMessages) >= 1 {
				cancel()
			}
			return nil
		})
		subscriptionDone <- err
	}()

	// Give subscription time to start
	time.Sleep(100 * time.Millisecond)

	// Publish message
	eventID, err := b.Publish(ctx, namespace, message)
	if err != nil {
		t.Fatalf("Failed to publish message: %v", err)
	}
	if eventID == "" {
		t.Fatal("Expected non-empty event ID")
	}

	// Wait for subscription to complete
	select {
	case err := <-subscriptionDone:
		if err != context.Canceled {
			t.Fatalf("Subscription error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscription did not complete within timeout")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(receivedMessages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(receivedMessages))
	}

	envelope := receivedMessages[0]
	if envelope.ID != eventID {
		t.Fatalf("Expected event ID %s, got %s", eventID, envelope.ID)
	}

	// Verify message content
	var receivedReq jsonrpc.Request
	if err := json.Unmarshal(envelope.Data, &receivedReq); err != nil {
		t.Fatalf("Failed to unmarshal received message: %v", err)
	}
	if receivedReq.Method != req.Method {
		t.Fatalf("Expected method %s, got %s", req.Method, receivedReq.Method)
	}
}

func testPublishAndSubscribeFromLastEventID(t *testing.T, factory BrokerFactory) {
	b := factory(t)
	defer cleanupBroker(t, b)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	namespace := "test-namespace-2"

	// Publish first message
	req1 := &jsonrpc.Request{
		JSONRPCVersion: "2.0",
		Method:         "test/method1",
		ID:             jsonrpc.NewRequestID(1),
	}
	req1Bytes, _ := json.Marshal(req1)
	eventID1, err := b.Publish(ctx, namespace, jsonrpc.Message(req1Bytes))
	if err != nil {
		t.Fatalf("Failed to publish first message: %v", err)
	}

	// Publish second message
	req2 := &jsonrpc.Request{
		JSONRPCVersion: "2.0",
		Method:         "test/method2",
		ID:             jsonrpc.NewRequestID(2),
	}
	req2Bytes, _ := json.Marshal(req2)
	eventID2, err := b.Publish(ctx, namespace, jsonrpc.Message(req2Bytes))
	if err != nil {
		t.Fatalf("Failed to publish second message: %v", err)
	}

	// Subscribe from first event ID (should receive second message only)
	var receivedMessages []broker.MessageEnvelope
	var mu sync.Mutex

	subscriptionDone := make(chan error, 1)
	go func() {
		err := b.Subscribe(ctx, namespace, eventID1, func(ctx context.Context, envelope broker.MessageEnvelope) error {
			mu.Lock()
			receivedMessages = append(receivedMessages, envelope)
			mu.Unlock()

			// Cancel after receiving one message
			if len(receivedMessages) >= 1 {
				cancel()
			}
			return nil
		})
		subscriptionDone <- err
	}()

	// Wait for subscription to complete
	select {
	case err := <-subscriptionDone:
		if err != context.Canceled {
			t.Fatalf("Subscription error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscription did not complete within timeout")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(receivedMessages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(receivedMessages))
	}

	envelope := receivedMessages[0]
	if envelope.ID != eventID2 {
		t.Fatalf("Expected event ID %s, got %s", eventID2, envelope.ID)
	}

	// Verify message content
	var receivedReq jsonrpc.Request
	if err := json.Unmarshal(envelope.Data, &receivedReq); err != nil {
		t.Fatalf("Failed to unmarshal received message: %v", err)
	}
	if receivedReq.Method != req2.Method {
		t.Fatalf("Expected method %s, got %s", req2.Method, receivedReq.Method)
	}
}

func testMultipleSubscribersToSameNamespace(t *testing.T, factory BrokerFactory) {
	b := factory(t)
	defer cleanupBroker(t, b)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	namespace := "test-namespace-3"

	// Create a test message
	req := &jsonrpc.Request{
		JSONRPCVersion: "2.0",
		Method:         "test/method",
		ID:             jsonrpc.NewRequestID(1),
	}
	reqBytes, _ := json.Marshal(req)
	message := jsonrpc.Message(reqBytes)

	var receivedMessages1, receivedMessages2 []broker.MessageEnvelope
	var mu1, mu2 sync.Mutex

	// Start first subscription
	subscription1Done := make(chan error, 1)
	go func() {
		err := b.Subscribe(ctx, namespace, "", func(ctx context.Context, envelope broker.MessageEnvelope) error {
			mu1.Lock()
			receivedMessages1 = append(receivedMessages1, envelope)
			mu1.Unlock()
			return nil
		})
		subscription1Done <- err
	}()

	// Start second subscription
	subscription2Done := make(chan error, 1)
	go func() {
		err := b.Subscribe(ctx, namespace, "", func(ctx context.Context, envelope broker.MessageEnvelope) error {
			mu2.Lock()
			receivedMessages2 = append(receivedMessages2, envelope)
			mu2.Unlock()
			return nil
		})
		subscription2Done <- err
	}()

	// Give subscriptions time to start
	time.Sleep(100 * time.Millisecond)

	// Publish message
	eventID, err := b.Publish(ctx, namespace, message)
	if err != nil {
		t.Fatalf("Failed to publish message: %v", err)
	}

	// Wait a bit for message delivery
	time.Sleep(200 * time.Millisecond)
	cancel()

	// Wait for subscriptions to complete
	select {
	case <-subscription1Done:
	case <-time.After(2 * time.Second):
		t.Fatal("First subscription did not complete within timeout")
	}
	select {
	case <-subscription2Done:
	case <-time.After(2 * time.Second):
		t.Fatal("Second subscription did not complete within timeout")
	}

	// Both subscribers should have received the message
	mu1.Lock()
	messages1Count := len(receivedMessages1)
	mu1.Unlock()

	mu2.Lock()
	messages2Count := len(receivedMessages2)
	mu2.Unlock()

	if messages1Count != 1 {
		t.Fatalf("First subscriber expected 1 message, got %d", messages1Count)
	}
	if messages2Count != 1 {
		t.Fatalf("Second subscriber expected 1 message, got %d", messages2Count)
	}

	mu1.Lock()
	if receivedMessages1[0].ID != eventID {
		t.Fatalf("First subscriber: expected event ID %s, got %s", eventID, receivedMessages1[0].ID)
	}
	mu1.Unlock()

	mu2.Lock()
	if receivedMessages2[0].ID != eventID {
		t.Fatalf("Second subscriber: expected event ID %s, got %s", eventID, receivedMessages2[0].ID)
	}
	mu2.Unlock()
}

func testNamespaceIsolation(t *testing.T, factory BrokerFactory) {
	b := factory(t)
	defer cleanupBroker(t, b)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	namespace1 := "test-namespace-4a"
	namespace2 := "test-namespace-4b"

	// Create test messages
	req1 := &jsonrpc.Request{
		JSONRPCVersion: "2.0",
		Method:         "test/method1",
		ID:             jsonrpc.NewRequestID(1),
	}
	req1Bytes, _ := json.Marshal(req1)

	req2 := &jsonrpc.Request{
		JSONRPCVersion: "2.0",
		Method:         "test/method2",
		ID:             jsonrpc.NewRequestID(2),
	}
	req2Bytes, _ := json.Marshal(req2)

	var receivedMessages1, receivedMessages2 []broker.MessageEnvelope
	var mu1, mu2 sync.Mutex

	// Subscribe to namespace1
	subscription1Done := make(chan error, 1)
	go func() {
		err := b.Subscribe(ctx, namespace1, "", func(ctx context.Context, envelope broker.MessageEnvelope) error {
			mu1.Lock()
			receivedMessages1 = append(receivedMessages1, envelope)
			mu1.Unlock()
			return nil
		})
		subscription1Done <- err
	}()

	// Subscribe to namespace2
	subscription2Done := make(chan error, 1)
	go func() {
		err := b.Subscribe(ctx, namespace2, "", func(ctx context.Context, envelope broker.MessageEnvelope) error {
			mu2.Lock()
			receivedMessages2 = append(receivedMessages2, envelope)
			mu2.Unlock()
			return nil
		})
		subscription2Done <- err
	}()

	// Give subscriptions time to start
	time.Sleep(100 * time.Millisecond)

	// Publish to namespace1
	_, err := b.Publish(ctx, namespace1, jsonrpc.Message(req1Bytes))
	if err != nil {
		t.Fatalf("Failed to publish to namespace1: %v", err)
	}

	// Publish to namespace2
	_, err = b.Publish(ctx, namespace2, jsonrpc.Message(req2Bytes))
	if err != nil {
		t.Fatalf("Failed to publish to namespace2: %v", err)
	}

	// Wait a bit for message delivery
	time.Sleep(200 * time.Millisecond)
	cancel()

	// Wait for subscriptions to complete
	select {
	case <-subscription1Done:
	case <-time.After(2 * time.Second):
		t.Fatal("First subscription did not complete within timeout")
	}
	select {
	case <-subscription2Done:
	case <-time.After(2 * time.Second):
		t.Fatal("Second subscription did not complete within timeout")
	}

	// Each subscriber should have received only their namespace's message
	mu1.Lock()
	messages1Count := len(receivedMessages1)
	mu1.Unlock()

	mu2.Lock()
	messages2Count := len(receivedMessages2)
	mu2.Unlock()

	if messages1Count != 1 {
		t.Fatalf("Namespace1 subscriber expected 1 message, got %d", messages1Count)
	}
	if messages2Count != 1 {
		t.Fatalf("Namespace2 subscriber expected 1 message, got %d", messages2Count)
	}

	// Verify message content isolation
	mu1.Lock()
	var receivedReq1 jsonrpc.Request
	if err := json.Unmarshal(receivedMessages1[0].Data, &receivedReq1); err != nil {
		t.Fatalf("Failed to unmarshal message from namespace1: %v", err)
	}
	if receivedReq1.Method != req1.Method {
		t.Fatalf("Namespace1: expected method %s, got %s", req1.Method, receivedReq1.Method)
	}
	mu1.Unlock()

	mu2.Lock()
	var receivedReq2 jsonrpc.Request
	if err := json.Unmarshal(receivedMessages2[0].Data, &receivedReq2); err != nil {
		t.Fatalf("Failed to unmarshal message from namespace2: %v", err)
	}
	if receivedReq2.Method != req2.Method {
		t.Fatalf("Namespace2: expected method %s, got %s", req2.Method, receivedReq2.Method)
	}
	mu2.Unlock()
}

func testSubscriptionContextCancellation(t *testing.T, factory BrokerFactory) {
	b := factory(t)
	defer cleanupBroker(t, b)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	namespace := "test-namespace-5"

	// Start subscription with short timeout
	subscriptionDone := make(chan error, 1)
	go func() {
		err := b.Subscribe(ctx, namespace, "", func(ctx context.Context, envelope broker.MessageEnvelope) error {
			return nil
		})
		subscriptionDone <- err
	}()

	// Wait for subscription to be cancelled by context timeout
	select {
	case err := <-subscriptionDone:
		if err != context.DeadlineExceeded {
			t.Fatalf("Expected context.DeadlineExceeded, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscription did not complete within timeout")
	}
}

func testHandlerErrorStopsSubscription(t *testing.T, factory BrokerFactory) {
	b := factory(t)
	defer cleanupBroker(t, b)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	namespace := "test-namespace-6"

	// Create a test message
	req := &jsonrpc.Request{
		JSONRPCVersion: "2.0",
		Method:         "test/method",
		ID:             jsonrpc.NewRequestID(1),
	}
	reqBytes, _ := json.Marshal(req)
	message := jsonrpc.Message(reqBytes)

	expectedErr := fmt.Errorf("handler error")

	// Start subscription that returns an error
	subscriptionDone := make(chan error, 1)
	go func() {
		err := b.Subscribe(ctx, namespace, "", func(ctx context.Context, envelope broker.MessageEnvelope) error {
			return expectedErr
		})
		subscriptionDone <- err
	}()

	// Give subscription time to start
	time.Sleep(100 * time.Millisecond)

	// Publish message
	_, err := b.Publish(ctx, namespace, message)
	if err != nil {
		t.Fatalf("Failed to publish message: %v", err)
	}

	// Wait for subscription to complete with error
	select {
	case err := <-subscriptionDone:
		if err != expectedErr {
			t.Fatalf("Expected handler error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscription did not complete within timeout")
	}
}

func testCleanup(t *testing.T, factory BrokerFactory) {
	b := factory(t)
	defer cleanupBroker(t, b)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	namespace := "test-namespace-7"

	// Publish a message
	req := &jsonrpc.Request{
		JSONRPCVersion: "2.0",
		Method:         "test/method",
		ID:             jsonrpc.NewRequestID(1),
	}
	reqBytes, _ := json.Marshal(req)
	message := jsonrpc.Message(reqBytes)

	eventID, err := b.Publish(ctx, namespace, message)
	if err != nil {
		t.Fatalf("Failed to publish message: %v", err)
	}

	// Cleanup namespace
	if err := b.Cleanup(ctx, namespace); err != nil {
		t.Fatalf("Failed to cleanup namespace: %v", err)
	}

	// Try to subscribe from the published event ID (should not find it)
	subscriptionCtx, subscriptionCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer subscriptionCancel()

	err = b.Subscribe(subscriptionCtx, namespace, eventID, func(ctx context.Context, envelope broker.MessageEnvelope) error {
		t.Fatal("Should not receive any messages after cleanup")
		return nil
	})

	// For memory implementation, this might return an error about event ID not found
	// For Redis implementation, this should just timeout without receiving messages
	if err != nil && err != context.DeadlineExceeded {
		// Some implementations may return an error for non-existent event ID, which is acceptable
		t.Logf("Subscription returned error after cleanup (acceptable): %v", err)
	}
}

func testResumeFromNonExistentEventID(t *testing.T, factory BrokerFactory) {
	b := factory(t)
	defer cleanupBroker(t, b)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	namespace := "test-namespace-8"
	nonExistentEventID := "non-existent-id"

	// Try to subscribe from non-existent event ID
	err := b.Subscribe(ctx, namespace, nonExistentEventID, func(ctx context.Context, envelope broker.MessageEnvelope) error {
		return nil
	})

	// Should return an error about event ID not found
	if err == nil {
		t.Fatal("Expected error for non-existent event ID, got nil")
	}
	if err == context.DeadlineExceeded {
		t.Fatal("Subscription should fail immediately for non-existent event ID, not timeout")
	}
}

// Helper functions

// cleanupBroker attempts to cleanup any test resources.
// This is a best-effort cleanup and errors are logged but not fatal.
func cleanupBroker(t *testing.T, b broker.Broker) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to cleanup known test namespaces
	namespaces := []string{
		"test-namespace", "test-namespace-2", "test-namespace-3",
		"test-namespace-4a", "test-namespace-4b", "test-namespace-5",
		"test-namespace-6", "test-namespace-7", "test-namespace-8",
	}

	for _, ns := range namespaces {
		if err := b.Cleanup(ctx, ns); err != nil {
			t.Logf("Warning: failed to cleanup namespace %s: %v", ns, err)
		}
	}

	// If broker has a Close method, call it
	if closer, ok := b.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			t.Logf("Warning: failed to close broker: %v", err)
		}
	}
}
