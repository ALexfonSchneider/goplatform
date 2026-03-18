package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerator_BaseOnly(t *testing.T) {
	tmp := t.TempDir()

	g := New()
	err := g.Render(tmp, Data{
		Name:   "testapp",
		Module: "github.com/user/testapp",
	})
	require.NoError(t, err)

	expectedFiles := []string{
		"cmd/main.go",
		"internal/handler/handler.go",
		"internal/service/service.go",
		"config/config.yaml",
		"Makefile",
		"Dockerfile",
		".golangci.yml",
	}

	for _, f := range expectedFiles {
		path := filepath.Join(tmp, f)
		_, err := os.Stat(path)
		assert.NoError(t, err, "expected file %s to exist", f)
	}

	// Verify component-specific files are NOT created.
	notExpected := []string{
		"internal/repository/repository.go",
		"migrations/000001_init.up.sql",
		"internal/consumer/consumer.go",
		"proto/service.proto",
	}

	for _, f := range notExpected {
		path := filepath.Join(tmp, f)
		_, err := os.Stat(path)
		assert.True(t, os.IsNotExist(err), "file %s should not exist", f)
	}
}

func TestGenerator_WithPostgres(t *testing.T) {
	tmp := t.TempDir()

	g := New()
	err := g.Render(tmp, Data{
		Name:     "pgapp",
		Module:   "github.com/user/pgapp",
		Postgres: true,
	})
	require.NoError(t, err)

	// Verify postgres-specific files exist.
	pgFiles := []string{
		"internal/repository/repository.go",
		"migrations/000001_init.up.sql",
	}
	for _, f := range pgFiles {
		path := filepath.Join(tmp, f)
		_, err := os.Stat(path)
		assert.NoError(t, err, "expected file %s to exist", f)
	}

	// Verify repository content references postgres.
	repo, err := os.ReadFile(filepath.Join(tmp, "internal/repository/repository.go"))
	require.NoError(t, err)
	assert.Contains(t, string(repo), "postgres")

	// Verify migration references the app name.
	migration, err := os.ReadFile(filepath.Join(tmp, "migrations/000001_init.up.sql"))
	require.NoError(t, err)
	assert.Contains(t, string(migration), "pgapp")
}

func TestGenerator_WithKafka(t *testing.T) {
	tmp := t.TempDir()

	g := New()
	err := g.Render(tmp, Data{
		Name:   "kafkaapp",
		Module: "github.com/user/kafkaapp",
		Kafka:  true,
	})
	require.NoError(t, err)

	consumerPath := filepath.Join(tmp, "internal/consumer/consumer.go")
	_, err = os.Stat(consumerPath)
	assert.NoError(t, err, "expected consumer.go to exist")

	content, err := os.ReadFile(consumerPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Kafka")
}

func TestGenerator_FullStack(t *testing.T) {
	tmp := t.TempDir()

	g := New()
	err := g.Render(tmp, Data{
		Name:     "fullapp",
		Module:   "github.com/user/fullapp",
		Postgres: true,
		Kafka:    true,
		NATS:     true,
		Redis:    true,
		Connect:  true,
	})
	require.NoError(t, err)

	allFiles := []string{
		// base
		"cmd/main.go",
		"internal/handler/handler.go",
		"internal/service/service.go",
		"config/config.yaml",
		"Makefile",
		"Dockerfile",
		".golangci.yml",
		// per-component configs
		"config/config.postgres.yaml",
		"config/config.kafka.yaml",
		"config/config.nats.yaml",
		"config/config.redis.yaml",
		// postgres
		"internal/repository/repository.go",
		"migrations/000001_init.up.sql",
		// connect
		"proto/service.proto",
	}

	for _, f := range allFiles {
		path := filepath.Join(tmp, f)
		_, err := os.Stat(path)
		assert.NoError(t, err, "expected file %s to exist", f)
	}

	// Verify per-component config files contain correct sections.
	pgCfg, err := os.ReadFile(filepath.Join(tmp, "config/config.postgres.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(pgCfg), "postgres:")

	redisCfg, err := os.ReadFile(filepath.Join(tmp, "config/config.redis.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(redisCfg), "redis:")

	kafkaCfg, err := os.ReadFile(filepath.Join(tmp, "config/config.kafka.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(kafkaCfg), "kafka:")

	natsCfg, err := os.ReadFile(filepath.Join(tmp, "config/config.nats.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(natsCfg), "nats:")

	// Verify proto file uses title case.
	proto, err := os.ReadFile(filepath.Join(tmp, "proto/service.proto"))
	require.NoError(t, err)
	assert.Contains(t, string(proto), "FullappService")
}

func TestGenerator_MainCompiles(t *testing.T) {
	tmp := t.TempDir()

	g := New()
	err := g.Render(tmp, Data{
		Name:     "compiletest",
		Module:   "github.com/user/compiletest",
		Postgres: true,
		Redis:    true,
	})
	require.NoError(t, err)

	mainContent, err := os.ReadFile(filepath.Join(tmp, "cmd/main.go"))
	require.NoError(t, err)
	mainStr := string(mainContent)

	// Verify module import is present.
	assert.Contains(t, mainStr, "github.com/user/compiletest/internal/handler")
	assert.Contains(t, mainStr, "github.com/user/compiletest/internal/service")

	// Verify no template syntax remains.
	assert.False(t, strings.Contains(mainStr, "{{"), "main.go should not contain template syntax")
	assert.False(t, strings.Contains(mainStr, "}}"), "main.go should not contain template syntax")

	// Verify conditional sections are present.
	assert.Contains(t, mainStr, "postgres")
	assert.Contains(t, mainStr, "redis")
	assert.Contains(t, mainStr, "COMPILETEST")
}
