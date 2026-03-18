package platformtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/server"
)

// NewTestServer creates a [server.Server] bound to a random available port,
// starts it, and returns both the server and its base URL (e.g.
// "http://127.0.0.1:54321").
//
// The random-port binding is achieved by prepending [server.WithAddr](":0") to
// the option list. Any user-supplied opts are applied after this default and
// may override the address if desired (though that is not the typical use
// case).
//
// A [testing.T.Cleanup] function is registered that gracefully stops the
// server with a 5-second timeout. This ensures the listening port is released
// and no goroutines are leaked after the test completes.
//
// NewTestServer calls [testing.T.Fatal] if the server cannot be created or
// started.
func NewTestServer(t *testing.T, opts ...server.Option) (*server.Server, string) {
	t.Helper()

	allOpts := make([]server.Option, 0, len(opts)+1)
	allOpts = append(allOpts, server.WithAddr(":0"))
	allOpts = append(allOpts, opts...)

	srv, err := server.New(allOpts...)
	require.NoError(t, err, "platformtest: create server")

	err = srv.Start(context.Background())
	require.NoError(t, err, "platformtest: start server")

	addr := srv.Addr()
	require.NotEmpty(t, addr, "platformtest: server addr must not be empty after start")

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if stopErr := srv.Stop(ctx); stopErr != nil {
			t.Errorf("platformtest: stop server: %v", stopErr)
		}
	})

	return srv, "http://" + addr
}
