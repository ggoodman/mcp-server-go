package memorybroker

import (
	"testing"

	"github.com/ggoodman/mcp-streaming-http-go/broker"
	"github.com/ggoodman/mcp-streaming-http-go/broker/brokertest"
)

func TestMemoryBroker(t *testing.T) {
	factory := func(t *testing.T) broker.Broker {
		return New()
	}

	brokertest.RunBrokerTests(t, factory)
}
