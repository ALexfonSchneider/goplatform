package platform

import (
	"log/slog"
)

// Logger defines a structured logging interface.
// Implementations must be safe for concurrent use.
type Logger interface {
	// Debug logs a message at debug level with optional key-value pairs.
	Debug(msg string, args ...any)

	// Info logs a message at info level with optional key-value pairs.
	Info(msg string, args ...any)

	// Warn logs a message at warn level with optional key-value pairs.
	Warn(msg string, args ...any)

	// Error logs a message at error level with optional key-value pairs.
	Error(msg string, args ...any)

	// With returns a new Logger that includes the given key-value pairs
	// in every subsequent log entry.
	With(args ...any) Logger
}

// slogLogger is a Logger backed by a *slog.Logger.
type slogLogger struct {
	logger *slog.Logger
}

// NewSlogLogger creates a Logger backed by the given slog.Handler.
func NewSlogLogger(handler slog.Handler) Logger {
	return &slogLogger{logger: slog.New(handler)}
}

// Debug logs a message at debug level.
func (l *slogLogger) Debug(msg string, args ...any) {
	l.logger.Debug(msg, args...)
}

// Info logs a message at info level.
func (l *slogLogger) Info(msg string, args ...any) {
	l.logger.Info(msg, args...)
}

// Warn logs a message at warn level.
func (l *slogLogger) Warn(msg string, args ...any) {
	l.logger.Warn(msg, args...)
}

// Error logs a message at error level.
func (l *slogLogger) Error(msg string, args ...any) {
	l.logger.Error(msg, args...)
}

// With returns a new Logger that includes the given key-value pairs.
func (l *slogLogger) With(args ...any) Logger {
	return &slogLogger{logger: l.logger.With(args...)}
}

// nopLogger is a Logger that discards all output.
type nopLogger struct{}

// NopLogger returns a Logger that discards all output.
func NopLogger() Logger {
	return &nopLogger{}
}

// Debug is a no-op.
func (n *nopLogger) Debug(_ string, _ ...any) {}

// Info is a no-op.
func (n *nopLogger) Info(_ string, _ ...any) {}

// Warn is a no-op.
func (n *nopLogger) Warn(_ string, _ ...any) {}

// Error is a no-op.
func (n *nopLogger) Error(_ string, _ ...any) {}

// With returns the same nopLogger since all output is discarded.
func (n *nopLogger) With(_ ...any) Logger {
	return n
}
