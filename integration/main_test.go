//go:build integration

package integration

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// kafka-go transport pool goroutines that don't shut down cleanly.
		goleak.IgnoreTopFunction("github.com/segmentio/kafka-go.(*connPool).discover"),
		goleak.IgnoreTopFunction("github.com/segmentio/kafka-go.(*conn).run"),
	)
}
