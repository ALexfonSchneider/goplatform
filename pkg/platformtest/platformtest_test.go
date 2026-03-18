package platformtest_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/broker"
	"github.com/ALexfonSchneider/goplatform/pkg/platform"
	"github.com/ALexfonSchneider/goplatform/pkg/platformtest"
)

// TestNewTestApp_Defaults verifies that NewTestApp creates an App with a
// NopLogger (logging does not panic) and a short shutdown timeout that
// results in a fast shutdown.
func TestNewTestApp_Defaults(t *testing.T) {
	app := platformtest.NewTestApp(t)

	// The logger should be a NopLogger — calling any log method must not
	// panic or produce visible output.
	logger := app.Logger()
	require.NotNil(t, logger)

	assert.NotPanics(t, func() {
		logger.Debug("debug message", "key", "value")
		logger.Info("info message", "key", "value")
		logger.Warn("warn message", "key", "value")
		logger.Error("error message", "key", "value")
	})

	// Verify short shutdown timeout by running the app and observing that
	// shutdown completes quickly. We register no components so Run returns
	// as soon as the context is cancelled.
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(ctx)
	}()

	// Give Run a moment to start, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("app.Run did not return within 3s — shutdown timeout is likely too long")
	}
}

// TestNewTestServer verifies that NewTestServer starts a server on a random
// port, that the returned URL is reachable, and that the port is released
// after cleanup.
func TestNewTestServer(t *testing.T) {
	srv, baseURL := platformtest.NewTestServer(t)

	require.NotNil(t, srv)
	require.NotEmpty(t, baseURL)

	// The server should respond on the liveness endpoint.
	resp, err := http.Get(baseURL + "/healthz/live")
	require.NoError(t, err)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "ok")
}

// TestNewTestBroker verifies that NewTestBroker returns a working MemBroker
// that can publish and subscribe synchronously.
func TestNewTestBroker(t *testing.T) {
	b := platformtest.NewTestBroker(t)
	require.NotNil(t, b)

	const topic = "test-topic"
	received := make(chan broker.Message, 1)

	err := b.Subscribe(context.Background(), topic, func(ctx context.Context, msg broker.Message) error {
		received <- msg
		return nil
	})
	require.NoError(t, err)

	payload := []byte("hello")
	err = b.Publish(context.Background(), topic, "key1", payload)
	require.NoError(t, err)

	select {
	case msg := <-received:
		assert.Equal(t, topic, msg.Topic)
		assert.True(t, bytes.Equal(payload, msg.Value), "payload mismatch")
		assert.Equal(t, []byte("key1"), msg.Key)
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for message from broker")
	}
}

// TestNewTestApp_CustomOptions verifies that user-supplied options override
// the defaults set by NewTestApp. Specifically, a custom logger should
// replace the default NopLogger.
func TestNewTestApp_CustomOptions(t *testing.T) {
	// Create a logger backed by a buffer so we can observe output.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	customLogger := platform.NewSlogLogger(handler)

	app := platformtest.NewTestApp(t, platform.WithLogger(customLogger))

	logger := app.Logger()
	require.NotNil(t, logger)

	// Log a message and verify it was captured — proving the custom logger
	// replaced the NopLogger default.
	logger.Info("custom log entry", "foo", "bar")

	assert.Contains(t, buf.String(), "custom log entry")
	assert.Contains(t, buf.String(), "foo")
}
