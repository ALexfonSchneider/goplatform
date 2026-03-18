package platformtest

import (
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// NewTestApp creates an [platform.App] configured for testing.
//
// Default options applied automatically:
//   - [platform.WithLogger](platform.NopLogger()) — suppresses log output so
//     test output stays clean.
//   - [platform.WithShutdownTimeout](1 * time.Second) — keeps tests fast by
//     limiting the graceful-shutdown window.
//
// Any user-supplied opts are applied after the defaults and therefore override
// them. For example, passing a custom logger replaces the NopLogger.
//
// A [testing.T.Cleanup] function is registered that checks for goroutine leaks
// via [go.uber.org/goleak]. Common goroutines from the runtime, testing, and
// signal-notification subsystems are automatically ignored.
func NewTestApp(t *testing.T, opts ...platform.Option) *platform.App {
	t.Helper()

	defaults := []platform.Option{
		platform.WithLogger(platform.NopLogger()),
		platform.WithShutdownTimeout(1 * time.Second),
	}

	// User opts come after defaults so they can override.
	allOpts := append(defaults, opts...)

	app := platform.New(allOpts...)

	t.Cleanup(func() {
		goleak.VerifyNone(
			t,
			goleak.IgnoreTopFunction("runtime.ensureSigM"),
			goleak.IgnoreTopFunction("os/signal.signal_recv"),
			goleak.IgnoreTopFunction("os/signal.loop"),
			goleak.IgnoreTopFunction("testing.(*T).Run"),
			goleak.IgnoreTopFunction("testing.tRunner"),
		)
	})

	return app
}
