//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/config"
)

// TestConfig_FileAndDefaults creates a YAML config file, loads it with defaults,
// and verifies the merged result.
func TestConfig_FileAndDefaults(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	// Create a temp YAML config file.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "app.yaml")
	yamlContent := `
server:
  addr: ":9090"
  read_timeout: "5s"
database:
  dsn: "postgres://user:pass@localhost:5432/mydb"
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(yamlContent), 0o644))

	type serverCfg struct {
		Addr        string `koanf:"addr"`
		ReadTimeout string `koanf:"read_timeout"`
	}
	type dbCfg struct {
		DSN      string `koanf:"dsn"`
		MaxConns int    `koanf:"max_conns"`
	}
	type appCfg struct {
		Server   serverCfg `koanf:"server"`
		Database dbCfg     `koanf:"database"`
	}

	loader, err := config.NewLoader(
		config.WithFile(cfgPath),
		config.WithDefaults(map[string]any{
			"server.addr":        ":8080",
			"database.max_conns": 10,
		}),
	)
	require.NoError(t, err)

	var cfg appCfg
	err = loader.Load(&cfg)
	require.NoError(t, err)

	// File values should override defaults.
	assert.Equal(t, ":9090", cfg.Server.Addr, "file value should override default")
	assert.Equal(t, "5s", cfg.Server.ReadTimeout)
	assert.Equal(t, "postgres://user:pass@localhost:5432/mydb", cfg.Database.DSN)
	// Default value should be present when not in file.
	assert.Equal(t, 10, cfg.Database.MaxConns, "default should apply when not in file")
}

// TestConfig_EnvOverride loads config from a file and then overrides with
// environment variables.
func TestConfig_EnvOverride(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "app.yaml")
	yamlContent := `
server:
  addr: ":8080"
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(yamlContent), 0o644))

	// Set env var to override file value.
	t.Setenv("TESTAPP__SERVER__ADDR", ":7070")

	type serverCfg struct {
		Addr string `koanf:"addr"`
	}
	type appCfg struct {
		Server serverCfg `koanf:"server"`
	}

	loader, err := config.NewLoader(
		config.WithFile(cfgPath),
		config.WithEnvPrefix("TESTAPP"),
	)
	require.NoError(t, err)

	var cfg appCfg
	err = loader.Load(&cfg)
	require.NoError(t, err)

	// Environment variable should override the file value.
	assert.Equal(t, ":7070", cfg.Server.Addr, "env var should override file value")
}

// TestConfig_MultipleFiles loads two config files where the second overrides
// values from the first.
func TestConfig_MultipleFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	dir := t.TempDir()
	base := filepath.Join(dir, "base.yaml")
	override := filepath.Join(dir, "override.yaml")

	require.NoError(t, os.WriteFile(base, []byte(`
server:
  addr: ":8080"
  read_timeout: "10s"
`), 0o644))

	require.NoError(t, os.WriteFile(override, []byte(`
server:
  addr: ":9090"
`), 0o644))

	type serverCfg struct {
		Addr        string `koanf:"addr"`
		ReadTimeout string `koanf:"read_timeout"`
	}
	type appCfg struct {
		Server serverCfg `koanf:"server"`
	}

	loader, err := config.NewLoader(
		config.WithFiles(base, override),
	)
	require.NoError(t, err)

	var cfg appCfg
	err = loader.Load(&cfg)
	require.NoError(t, err)

	assert.Equal(t, ":9090", cfg.Server.Addr, "override file should take precedence")
	assert.Equal(t, "10s", cfg.Server.ReadTimeout, "base value should remain when not overridden")
}
