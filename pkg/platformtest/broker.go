package platformtest

import (
	"testing"

	"github.com/ALexfonSchneider/goplatform/pkg/broker/membroker"
)

// NewTestBroker creates an in-memory [membroker.MemBroker] suitable for unit
// tests.
//
// The returned broker is synchronous: [membroker.MemBroker.Publish] blocks
// until every registered handler has finished processing the message. This
// makes tests deterministic and eliminates the need for polling or timeouts
// when asserting on consumed messages.
//
// The [testing.T] parameter is accepted for consistency with the other
// NewTest* helpers and for future extensibility (e.g. registering cleanup
// hooks). Currently no cleanup is needed because MemBroker does not spawn
// background goroutines.
func NewTestBroker(t *testing.T) *membroker.MemBroker {
	t.Helper()

	return membroker.New()
}
