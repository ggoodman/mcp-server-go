package redisbroker

import (
	"context"
	"testing"

	"github.com/ggoodman/mcp-streaming-http-go/broker"
	"github.com/ggoodman/mcp-streaming-http-go/broker/brokertest"
	"github.com/redis/go-redis/v9"
)

func TestRedisBroker(t *testing.T) {
	// Skip if Redis is not available
	testClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// Test Redis connection
	if err := testClient.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	testClient.Close() // Close the test client

	factory := func(t *testing.T) broker.Broker {
		// Create a fresh client for each test run
		client := redis.NewClient(&redis.Options{
			Addr: "localhost:6379",
		})
		return New(Config{
			Client:    client,
			KeyPrefix: "test:broker:",
		})
	}

	brokertest.RunBrokerTests(t, factory)
}
