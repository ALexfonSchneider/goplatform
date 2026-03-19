//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	platformv1 "github.com/ALexfonSchneider/goplatform/gen/platform/v1"
	"github.com/ALexfonSchneider/goplatform/gen/platform/v1/platformv1connect"
	"github.com/ALexfonSchneider/goplatform/pkg/server"
)

// healthService implements the HealthServiceHandler for testing.
type healthService struct {
	platformv1connect.UnimplementedHealthServiceHandler
}

func (h *healthService) Check(_ context.Context, _ *connect.Request[platformv1.CheckRequest]) (*connect.Response[platformv1.CheckResponse], error) {
	return connect.NewResponse(&platformv1.CheckResponse{
		Status: "SERVING",
	}), nil
}

// TestServer_ConnectRPCAndREST creates a Server with both a REST route and a
// ConnectRPC HealthService handler on the same port. It verifies both respond
// correctly, proving single-port serving works.
func TestServer_ConnectRPCAndREST(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	srv, err := server.New(
		server.WithAddr(":0"),
	)
	require.NoError(t, err)

	// Mount REST route.
	srv.Route("/api/v1", func(r chi.Router) {
		r.Get("/hello", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "hello"})
		})
	})

	// Mount ConnectRPC HealthService handler.
	path, handler := platformv1connect.NewHealthServiceHandler(&healthService{})
	srv.Mount(path, handler)

	ctx := context.Background()
	require.NoError(t, srv.Start(ctx))
	defer func() { require.NoError(t, srv.Stop(ctx)) }()

	addr := srv.Addr()
	require.NotEmpty(t, addr, "server should have a listen address")
	baseURL := fmt.Sprintf("http://%s", addr)

	// Test REST endpoint.
	t.Run("REST", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/v1/hello")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var body map[string]string
		err = json.NewDecoder(resp.Body).Decode(&body)
		require.NoError(t, err)
		assert.Equal(t, "hello", body["message"])
	})

	// Test ConnectRPC endpoint.
	t.Run("ConnectRPC", func(t *testing.T) {
		client := platformv1connect.NewHealthServiceClient(
			http.DefaultClient,
			baseURL,
		)

		resp, err := client.Check(ctx, connect.NewRequest(&platformv1.CheckRequest{}))
		require.NoError(t, err)
		assert.Equal(t, "SERVING", resp.Msg.Status)
	})

	// Test built-in liveness endpoint.
	t.Run("Liveness", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/healthz/live")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var body map[string]any
		err = json.NewDecoder(resp.Body).Decode(&body)
		require.NoError(t, err)
		assert.Equal(t, "ok", body["status"])
	})
}
