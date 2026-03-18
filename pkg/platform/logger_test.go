package platform

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNopLogger(t *testing.T) {
	l := NopLogger()
	ctx := context.Background()

	// None of these should panic.
	l.Debug("debug msg", "k", "v")
	l.Info("info msg", "k", "v")
	l.Warn("warn msg", "k", "v")
	l.Error("error msg", "k", "v")

	// Context variants should not panic either.
	l.DebugContext(ctx, "debug ctx", "k", "v")
	l.InfoContext(ctx, "info ctx", "k", "v")
	l.WarnContext(ctx, "warn ctx", "k", "v")
	l.ErrorContext(ctx, "error ctx", "k", "v")

	child := l.With("parent", "value")
	require.NotNil(t, child)
	child.Info("child msg")
}

func TestSlogLogger(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	l := NewSlogLogger(handler)

	l.Info("hello world", "key1", "val1", "key2", 42)

	output := buf.String()
	assert.Contains(t, output, "hello world")
	assert.Contains(t, output, "key1")
	assert.Contains(t, output, "val1")
	assert.Contains(t, output, "key2")
	assert.Contains(t, output, "42")
}

func TestSlogLoggerWith(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	l := NewSlogLogger(handler)

	child := l.With("component", "db")
	require.NotNil(t, child)

	child.Info("connected", "host", "localhost")

	output := buf.String()
	// Child logger should include parent's fields.
	assert.Contains(t, output, "component")
	assert.Contains(t, output, "db")
	// And its own message + fields.
	assert.Contains(t, output, "connected")
	assert.Contains(t, output, "host")
	assert.Contains(t, output, "localhost")
}

func TestSlogLoggerContext(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	l := NewSlogLogger(handler)

	ctx := context.Background()
	l.InfoContext(ctx, "with context", "action", "test")

	output := buf.String()
	assert.Contains(t, output, "with context")
	assert.Contains(t, output, "action")
	assert.Contains(t, output, "test")
}
