// Package main demonstrates a complete service using goplatform.
//
// It wires together:
//   - Platform App with lifecycle management
//   - HTTP server (chi) + health endpoints
//   - Config loader (yaml + env)
//   - Structured logging (slog)
//
// Run: go run ./examples/orderservice/
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/ALexfonSchneider/goplatform/pkg/config"
	"github.com/ALexfonSchneider/goplatform/pkg/platform"
	"github.com/ALexfonSchneider/goplatform/pkg/server"
)

type appConfig struct {
	Server struct {
		Addr string `koanf:"addr"`
	} `koanf:"server"`
}

func main() {
	logger := platform.NewSlogLogger(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	app := platform.New(
		platform.WithLogger(logger),
	)

	// Load configuration.
	loader, err := config.NewLoader(
		config.WithDefaults(map[string]any{
			"server.addr": ":8080",
		}),
		config.WithFile("examples/orderservice/config.yaml"),
		config.WithEnvPrefix("ORDER"),
	)
	if err != nil {
		logger.Error("config loader error", "error", err)
		os.Exit(1)
	}

	var cfg appConfig
	if err := loader.Load(&cfg); err != nil {
		logger.Error("config load error", "error", err)
		os.Exit(1)
	}

	// HTTP Server.
	srv, err := server.New(
		server.WithAddr(cfg.Server.Addr),
		server.WithLogger(logger),
	)
	if err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}

	// Register routes.
	srv.Route("/api/v1", func(r chi.Router) {
		r.Get("/orders", listOrders)
		r.Post("/orders", createOrder)
		r.Get("/orders/{id}", getOrder)
	})

	_ = app.Register("server", srv)

	logger.Info("starting orderservice", "addr", cfg.Server.Addr)
	if err := app.Run(context.Background()); err != nil {
		logger.Error("application error", "error", err)
		os.Exit(1)
	}
}

func listOrders(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"orders":[]}`))
}

func createOrder(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"id":"ord-001","status":"created"}`))
}

func getOrder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "pending"})
}
