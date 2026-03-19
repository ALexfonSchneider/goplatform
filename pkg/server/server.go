// Package server provides an HTTP server built on chi that implements platform.Component.
//
// Server manages a single HTTP port serving REST endpoints, ConnectRPC handlers,
// health checks, and metrics. ConnectRPC handlers are standard http.Handlers and
// are mounted via Server.Mount() — no separate gRPC listener needed.
//
// Usage:
//
//	srv, _ := server.New(
//	    server.WithAddr(":8080"),
//	    server.WithLogger(logger),
//	    server.WithHealthCheckers(app.HealthCheckers()),
//	)
//	// Mount REST routes
//	srv.Route("/api/v1", func(r chi.Router) {
//	    r.Get("/orders", listOrders)
//	})
//	// Mount ConnectRPC handler
//	path, handler := orderv1connect.NewOrderServiceHandler(svc)
//	srv.Mount(path, handler)
//	// Register as platform component
//	app.Register("server", srv)
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// Compile-time check that Server implements platform.Component.
var _ platform.Component = (*Server)(nil)

// Server is an HTTP server built on chi that implements platform.Component.
// It manages the full lifecycle: listening, serving, and graceful shutdown.
//
// Default middleware (Recovery, RequestLogging) are applied automatically
// and can be disabled via WithoutRecovery() / WithoutRequestLogging().
// Additional middleware can be added via Use().
//
// Health endpoints are auto-mounted:
//   - GET /healthz/live  — always returns 200 (liveness probe for K8s)
//   - GET /healthz/ready — checks all registered HealthCheckers (readiness probe)
//   - GET /metrics       — placeholder for OTel metrics export
type Server struct {
	mu sync.Mutex

	// Configuration set via options.
	addr                  string
	readTimeout           time.Duration
	writeTimeout          time.Duration
	logger                platform.Logger
	checkers              map[string]platform.HealthChecker
	disableRecovery       bool
	disableRequestLogging bool

	// Pending middleware/routes added between New() and Start().
	// Applied in Start() in correct order: default mw → user mw → routes → health.
	pendingMiddleware []func(http.Handler) http.Handler
	pendingMounts     []mountEntry
	pendingRoutes     []routeEntry
	built             bool // true after Start() has built the router

	// Runtime state.
	router   chi.Router   // chi router with all routes and middleware
	httpSrv  *http.Server // created in Start, used for graceful Shutdown
	listener net.Listener // TCP listener, created in Start
}

type mountEntry struct {
	pattern string
	handler http.Handler
}

type routeEntry struct {
	pattern string
	fn      func(chi.Router)
}

// Option configures a Server.
type Option func(*Server)

// New creates a new Server with the given options.
func New(opts ...Option) (*Server, error) {
	s := &Server{
		addr:         ":8080",
		readTimeout:  10 * time.Second,
		writeTimeout: 10 * time.Second,
		logger:       platform.NopLogger(),
		router:       chi.NewRouter(),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s, nil
}

// WithAddr returns an Option that sets the listen address.
func WithAddr(addr string) Option {
	return func(s *Server) {
		s.addr = addr
	}
}

// WithReadTimeout returns an Option that sets the HTTP read timeout.
func WithReadTimeout(d time.Duration) Option {
	return func(s *Server) {
		s.readTimeout = d
	}
}

// WithWriteTimeout returns an Option that sets the HTTP write timeout.
func WithWriteTimeout(d time.Duration) Option {
	return func(s *Server) {
		s.writeTimeout = d
	}
}

// WithLogger returns an Option that sets the server's logger.
func WithLogger(l platform.Logger) Option {
	return func(s *Server) {
		s.logger = l
	}
}

// WithHealthCheckers returns an Option that sets the health checkers used
// by the readiness endpoint.
func WithHealthCheckers(checkers map[string]platform.HealthChecker) Option {
	return func(s *Server) {
		s.checkers = checkers
	}
}

// WithoutRecovery disables the default panic-recovery middleware.
func WithoutRecovery() Option {
	return func(s *Server) {
		s.disableRecovery = true
	}
}

// WithoutRequestLogging disables the default request-logging middleware.
func WithoutRequestLogging() Option {
	return func(s *Server) {
		s.disableRequestLogging = true
	}
}

// Start begins listening and serving HTTP requests. It implements platform.Component.
//
// Start builds the router in the correct order to satisfy chi's requirement
// that all middleware must be defined before routes:
//  1. Default middleware (Recovery, RequestLogging)
//  2. User middleware (added via Use())
//  3. User routes (added via Mount(), Route())
//  4. Health endpoints (/healthz/live, /healthz/ready, /metrics)
func (s *Server) Start(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Build router: middleware first, then routes.
	if !s.disableRecovery {
		s.router.Use(Recovery(s.logger))
	}
	if !s.disableRequestLogging {
		s.router.Use(RequestLogging(s.logger, "/healthz/live", "/healthz/ready", "/metrics"))
	}
	s.router.Use(s.pendingMiddleware...)
	for _, m := range s.pendingMounts {
		s.router.Mount(m.pattern, m.handler)
	}
	for _, r := range s.pendingRoutes {
		s.router.Route(r.pattern, r.fn)
	}
	s.mountHealthEndpoints()
	s.built = true

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("server: listen on %s: %w", s.addr, err)
	}
	s.listener = ln

	s.httpSrv = &http.Server{
		Handler:      s.router,
		ReadTimeout:  s.readTimeout,
		WriteTimeout: s.writeTimeout,
		IdleTimeout:  60 * time.Second,
	}

	ready := make(chan struct{})

	go func() {
		close(ready)
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("server: serve error", "error", err)
		}
	}()

	<-ready

	s.logger.Info("server: started", "addr", ln.Addr().String())
	return nil
}

// Stop gracefully shuts down the server. It implements platform.Component.
// In-flight requests are allowed to complete; new requests are rejected.
// Stop respects the ctx deadline for the graceful shutdown window.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	srv := s.httpSrv
	s.mu.Unlock()

	if srv == nil {
		return nil
	}

	s.logger.Info("server: stopping")

	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("server: shutdown: %w", err)
	}

	s.logger.Info("server: stopped")
	return nil
}

// Mount mounts an http.Handler at the given pattern. This is the primary way
// to attach ConnectRPC services — ConnectRPC handlers implement http.Handler:
//
//	path, handler := myv1connect.NewMyServiceHandler(svc, connect.WithInterceptors(...))
//	srv.Mount(path, handler)
func (s *Server) Mount(pattern string, handler http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.built {
		s.router.Mount(pattern, handler)
	} else {
		s.pendingMounts = append(s.pendingMounts, mountEntry{pattern, handler})
	}
}

// Route adds routes via a chi.Router function.
func (s *Server) Route(pattern string, fn func(chi.Router)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.built {
		s.router.Route(pattern, fn)
	} else {
		s.pendingRoutes = append(s.pendingRoutes, routeEntry{pattern, fn})
	}
}

// Use adds middleware to the server's middleware stack. These are applied
// after the default middleware (Recovery, RequestLogging) and affect all routes
// including health endpoints. Must be called before Start().
func (s *Server) Use(middlewares ...func(http.Handler) http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.built {
		s.router.Use(middlewares...)
	} else {
		s.pendingMiddleware = append(s.pendingMiddleware, middlewares...)
	}
}

// Router returns the underlying chi.Router for direct access.
func (s *Server) Router() chi.Router {
	return s.router
}

// Addr returns the address the server is listening on. It returns an empty
// string if the server has not been started.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// mountHealthEndpoints registers the /healthz/live, /healthz/ready, and
// /metrics endpoints on the router.
func (s *Server) mountHealthEndpoints() {
	s.router.Get("/healthz/live", s.handleLive)
	s.router.Get("/healthz/ready", s.handleReady)
	s.router.Get("/metrics", s.handleMetrics)
}

// healthResponse represents the JSON body returned by health endpoints.
type healthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// handleLive handles GET /healthz/live. It always returns 200 with {"status":"ok"}.
func (s *Server) handleLive(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
}

// handleReady handles GET /healthz/ready. It checks all HealthCheckers and
// returns 200 if all are ok, or 503 if any fail.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	resp := healthResponse{
		Status: "ok",
		Checks: make(map[string]string),
	}

	if s.checkers != nil {
		for name, checker := range s.checkers {
			if err := checker.HealthCheck(r.Context()); err != nil {
				resp.Status = "degraded"
				resp.Checks[name] = err.Error()
			} else {
				resp.Checks[name] = "ok"
			}
		}
	}

	if resp.Status == "ok" {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	_ = json.NewEncoder(w).Encode(resp)
}

// handleMetrics handles GET /metrics. It is a placeholder that will be wired
// to OpenTelemetry later.
func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("# metrics placeholder\n"))
}
