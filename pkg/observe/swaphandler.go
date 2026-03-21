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
//
// WithAttrs and WithGroup return derived handlers that share the same root
// pointer. When swap() is called on the root, all derived handlers
// automatically pick up the new underlying handler.
type swapHandler struct {
	root   *atomic.Pointer[slog.Handler]
	builds []func(slog.Handler) slog.Handler
}

func newSwapHandler(h slog.Handler) *swapHandler {
	root := &atomic.Pointer[slog.Handler]{}
	root.Store(&h)
	return &swapHandler{root: root}
}

func (s *swapHandler) swap(h slog.Handler) {
	s.root.Store(&h)
}

func (s *swapHandler) current() slog.Handler {
	h := *s.root.Load()
	for _, fn := range s.builds {
		h = fn(h)
	}
	return h
}

func (s *swapHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return s.current().Enabled(ctx, level)
}

func (s *swapHandler) Handle(ctx context.Context, r slog.Record) error {
	return s.current().Handle(ctx, r)
}

func (s *swapHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	builds := make([]func(slog.Handler) slog.Handler, len(s.builds), len(s.builds)+1)
	copy(builds, s.builds)
	builds = append(builds, func(h slog.Handler) slog.Handler {
		return h.WithAttrs(attrs)
	})
	return &swapHandler{root: s.root, builds: builds}
}

func (s *swapHandler) WithGroup(name string) slog.Handler {
	builds := make([]func(slog.Handler) slog.Handler, len(s.builds), len(s.builds)+1)
	copy(builds, s.builds)
	builds = append(builds, func(h slog.Handler) slog.Handler {
		return h.WithGroup(name)
	})
	return &swapHandler{root: s.root, builds: builds}
}
