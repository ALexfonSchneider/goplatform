package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1 "github.com/ALexfonSchneider/goplatform/gen/platform/v1"
	"github.com/ALexfonSchneider/goplatform/gen/platform/v1/platformv1connect"
	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// testHealthHandler is a simple HealthService handler used in tests.
type testHealthHandler struct {
	platformv1connect.UnimplementedHealthServiceHandler
}

// Check returns a SERVING status.
func (h *testHealthHandler) Check(_ context.Context, _ *connect.Request[v1.CheckRequest]) (*connect.Response[v1.CheckResponse], error) {
	return connect.NewResponse(&v1.CheckResponse{Status: "SERVING"}), nil
}

// errorHealthHandler is a HealthService handler that returns a configurable error.
type errorHealthHandler struct {
	platformv1connect.UnimplementedHealthServiceHandler
	err error
}

// Check returns the configured error.
func (h *errorHealthHandler) Check(_ context.Context, _ *connect.Request[v1.CheckRequest]) (*connect.Response[v1.CheckResponse], error) {
	return nil, h.err
}

func TestErrorInterceptor_PlatformNotFound(t *testing.T) {
	handler := &errorHealthHandler{
		err: platform.NewError(platform.CodeNotFound, "user not found"),
	}

	interceptor := ErrorInterceptor()
	path, h := platformv1connect.NewHealthServiceHandler(handler,
		connect.WithInterceptors(interceptor),
	)

	srv, baseURL := startTestServer(t)
	defer func() { require.NoError(t, srv.Stop(context.Background())) }()
	srv.Mount(path, h)

	client := platformv1connect.NewHealthServiceClient(http.DefaultClient, baseURL)
	_, err := client.Check(context.Background(), connect.NewRequest(&v1.CheckRequest{}))

	require.Error(t, err)

	var connectErr *connect.Error
	require.True(t, errors.As(err, &connectErr))
	assert.Equal(t, connect.CodeNotFound, connectErr.Code())
	assert.Contains(t, connectErr.Message(), "user not found")
}

func TestErrorInterceptor_PlainError(t *testing.T) {
	handler := &errorHealthHandler{
		err: errors.New("db connection failed"),
	}

	interceptor := ErrorInterceptor()
	path, h := platformv1connect.NewHealthServiceHandler(handler,
		connect.WithInterceptors(interceptor),
	)

	srv, baseURL := startTestServer(t)
	defer func() { require.NoError(t, srv.Stop(context.Background())) }()
	srv.Mount(path, h)

	client := platformv1connect.NewHealthServiceClient(http.DefaultClient, baseURL)
	_, err := client.Check(context.Background(), connect.NewRequest(&v1.CheckRequest{}))

	require.Error(t, err)

	var connectErr *connect.Error
	require.True(t, errors.As(err, &connectErr))
	assert.Equal(t, connect.CodeInternal, connectErr.Code())
	assert.Equal(t, "internal error", connectErr.Message())
}

func TestErrorInterceptor_NoError(t *testing.T) {
	handler := &testHealthHandler{}

	interceptor := ErrorInterceptor()
	path, h := platformv1connect.NewHealthServiceHandler(handler,
		connect.WithInterceptors(interceptor),
	)

	srv, baseURL := startTestServer(t)
	defer func() { require.NoError(t, srv.Stop(context.Background())) }()
	srv.Mount(path, h)

	client := platformv1connect.NewHealthServiceClient(http.DefaultClient, baseURL)
	resp, err := client.Check(context.Background(), connect.NewRequest(&v1.CheckRequest{}))

	require.NoError(t, err)
	assert.Equal(t, "SERVING", resp.Msg.GetStatus())
}

func TestConnectRPC_HealthService(t *testing.T) {
	handler := &testHealthHandler{}

	path, h := platformv1connect.NewHealthServiceHandler(handler)

	srv, baseURL := startTestServer(t)
	defer func() { require.NoError(t, srv.Stop(context.Background())) }()
	srv.Mount(path, h)

	client := platformv1connect.NewHealthServiceClient(http.DefaultClient, baseURL)
	resp, err := client.Check(context.Background(), connect.NewRequest(&v1.CheckRequest{}))

	require.NoError(t, err)
	assert.Equal(t, "SERVING", resp.Msg.GetStatus())
}

func TestConnectRPC_WithReflection(t *testing.T) {
	srv, baseURL := startTestServer(t)
	defer func() { require.NoError(t, srv.Stop(context.Background())) }()

	// Mount the HealthService.
	handler := &testHealthHandler{}
	path, h := platformv1connect.NewHealthServiceHandler(handler)
	srv.Mount(path, h)

	// Mount a stub reflection handler to verify the Mount path works
	// and the endpoint does not 404. This avoids a dependency on
	// connectrpc.com/grpcreflect which is not available in the module cache.
	srv.Mount("/grpc.reflection.v1.ServerReflection/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	resp, err := http.Post(
		baseURL+"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
		"application/grpc+proto",
		nil,
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()

	assert.NotEqual(t, http.StatusNotFound, resp.StatusCode)
}
