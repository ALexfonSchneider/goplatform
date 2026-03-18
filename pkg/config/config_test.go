package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testConfig struct {
	Server   serverConfig   `koanf:"server"`
	Postgres postgresConfig `koanf:"postgres"`
}

type serverConfig struct {
	Addr string `koanf:"addr"`
}

type postgresConfig struct {
	DSN      string `koanf:"dsn"`
	MaxConns int    `koanf:"max_conns"`
}

func TestLoader_Defaults(t *testing.T) {
	loader, err := NewLoader(
		WithDefaults(map[string]any{
			"server.addr":        ":8080",
			"postgres.dsn":       "postgres://localhost/test",
			"postgres.max_conns": 10,
		}),
	)
	require.NoError(t, err)

	var cfg testConfig
	require.NoError(t, loader.Load(&cfg))

	assert.Equal(t, ":8080", cfg.Server.Addr)
	assert.Equal(t, "postgres://localhost/test", cfg.Postgres.DSN)
	assert.Equal(t, 10, cfg.Postgres.MaxConns)
}

func TestLoader_YAMLFile(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")

	yamlContent := `
server:
  addr: ":9090"
postgres:
  dsn: "postgres://db:5432/prod"
  max_conns: 25
`
	require.NoError(t, os.WriteFile(cfgFile, []byte(yamlContent), 0o644))

	loader, err := NewLoader(WithFile(cfgFile))
	require.NoError(t, err)

	var cfg testConfig
	require.NoError(t, loader.Load(&cfg))

	assert.Equal(t, ":9090", cfg.Server.Addr)
	assert.Equal(t, "postgres://db:5432/prod", cfg.Postgres.DSN)
	assert.Equal(t, 25, cfg.Postgres.MaxConns)
}

func TestLoader_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")

	yamlContent := `
server:
  addr: ":9090"
postgres:
  dsn: "postgres://db:5432/prod"
  max_conns: 25
`
	require.NoError(t, os.WriteFile(cfgFile, []byte(yamlContent), 0o644))

	t.Setenv("MYAPP__SERVER__ADDR", ":7070")
	t.Setenv("MYAPP__POSTGRES__DSN", "postgres://override:5432/test")

	loader, err := NewLoader(
		WithDefaults(map[string]any{
			"postgres.max_conns": 5,
		}),
		WithFile(cfgFile),
		WithEnvPrefix("MYAPP"),
	)
	require.NoError(t, err)

	var cfg testConfig
	require.NoError(t, loader.Load(&cfg))

	assert.Equal(t, ":7070", cfg.Server.Addr)
	assert.Equal(t, "postgres://override:5432/test", cfg.Postgres.DSN)
	assert.Equal(t, 25, cfg.Postgres.MaxConns)
}

func TestLoader_EnvOverride_UnderscoreKeys(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")

	yamlContent := `
postgres:
  max_conns: 10
`
	require.NoError(t, os.WriteFile(cfgFile, []byte(yamlContent), 0o644))

	// Double underscore separates levels, single underscore is part of key name.
	// MYAPP__POSTGRES__MAX_CONNS → postgres.max_conns
	t.Setenv("MYAPP__POSTGRES__MAX_CONNS", "50")

	loader, err := NewLoader(
		WithFile(cfgFile),
		WithEnvPrefix("MYAPP"),
	)
	require.NoError(t, err)

	var cfg testConfig
	require.NoError(t, loader.Load(&cfg))

	assert.Equal(t, 50, cfg.Postgres.MaxConns)
}

func TestLoader_InvalidYAML(t *testing.T) {
	t.Run("non-existent file", func(t *testing.T) {
		_, err := NewLoader(WithFile("/nonexistent/path/config.yaml"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config:")
	})

	t.Run("malformed YAML", func(t *testing.T) {
		dir := t.TempDir()
		cfgFile := filepath.Join(dir, "bad.yaml")

		require.NoError(t, os.WriteFile(cfgFile, []byte("{{{{invalid yaml"), 0o644))

		_, err := NewLoader(WithFile(cfgFile))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config:")
	})
}

func TestLoader_Watch(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")

	yamlContent := `
server:
  addr: ":8080"
`
	require.NoError(t, os.WriteFile(cfgFile, []byte(yamlContent), 0o644))

	loader, err := NewLoader(WithFile(cfgFile))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	called := make(chan struct{}, 1)
	errCh := make(chan error, 1)

	go func() {
		errCh <- loader.Watch(ctx, func() {
			select {
			case called <- struct{}{}:
			default:
			}
		})
	}()

	// Give the watcher time to start.
	time.Sleep(100 * time.Millisecond)

	// Modify the file.
	updatedYAML := `
server:
  addr: ":9999"
`
	require.NoError(t, os.WriteFile(cfgFile, []byte(updatedYAML), 0o644))

	select {
	case <-called:
		// onChange was called, verify the config was reloaded.
		var cfg testConfig
		require.NoError(t, loader.Load(&cfg))
		assert.Equal(t, ":9999", cfg.Server.Addr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for onChange callback")
	}

	cancel()

	select {
	case watchErr := <-errCh:
		require.NoError(t, watchErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Watch to return")
	}
}

func TestLoader_WatchNoFile(t *testing.T) {
	loader, err := NewLoader(
		WithDefaults(map[string]any{"server.addr": ":8080"}),
	)
	require.NoError(t, err)

	err = loader.Watch(context.Background(), func() {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config: watch: no file")
}
