package observe

import (
	"context"
	"log/slog"
	"sync/atomic"
)

// swapHandler is an slog.Handler that delegates to an atomically-swappable
// underlying handler. This allows the Observer to upgrade from base-only
// (stdout) to combined (stdout + OTel) after Start() without requiring
// callers to re-obtain their Logger.
type swapHandler struct {
	handler atomic.Pointer[slog.Handler]
}

func newSwapHandler(h slog.Handler) *swapHandler {
	sh := &swapHandler{}
	sh.handler.Store(&h)
	return sh
}

func (s *swapHandler) swap(h slog.Handler) {
	s.handler.Store(&h)
}

func (s *swapHandler) current() slog.Handler {
	return *s.handler.Load()
}

func (s *swapHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return s.current().Enabled(ctx, level)
}

func (s *swapHandler) Handle(ctx context.Context, r slog.Record) error {
	return s.current().Handle(ctx, r)
}

func (s *swapHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return s.current().WithAttrs(attrs)
}

func (s *swapHandler) WithGroup(name string) slog.Handler {
	return s.current().WithGroup(name)
}
