# Broker Test Suite

This package provides a comprehensive test suite for broker implementations that conform to the `broker.Broker` interface.

## Usage

To test your broker implementation, create a factory function and call `RunBrokerTests`:

```go
package mybroker

import (
    "testing"
    "github.com/ggoodman/mcp-streaming-http-go/broker"
    "github.com/ggoodman/mcp-streaming-http-go/broker/brokertest"
)

func TestMyBroker(t *testing.T) {
    factory := func(t *testing.T) broker.Broker {
        return NewMyBroker(/* config */)
    }

    brokertest.RunBrokerTests(t, factory)
}
```

## Test Coverage

The test suite covers the following scenarios:

- **PublishAndSubscribeFromBeginning**: Tests basic publish/subscribe functionality
- **PublishAndSubscribeFromLastEventID**: Tests resuming subscription from a specific event ID
- **MultipleSubscribersToSameNamespace**: Tests that multiple subscribers receive the same messages
- **NamespaceIsolation**: Tests that messages published to one namespace don't leak to another
- **SubscriptionContextCancellation**: Tests proper cleanup when subscription context is cancelled
- **HandlerErrorStopsSubscription**: Tests that handler errors properly terminate subscriptions
- **Cleanup**: Tests namespace resource cleanup
- **ResumeFromNonExistentEventID**: Tests error handling for invalid resume event IDs

## Requirements

Your broker implementation must:

1. Implement the `broker.Broker` interface exactly
2. Handle `jsonrpc.Message` types correctly (they are already `[]byte`, don't double-marshal)
3. Provide proper error propagation from message handlers
4. Support namespace isolation
5. Generate unique, monotonically increasing event IDs within each namespace
6. Support resuming from specific event IDs

## Optional Interface

If your broker implements a `Close() error` method, the test suite will call it during cleanup to properly release resources.
