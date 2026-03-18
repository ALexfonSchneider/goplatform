package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitCommand_CreatesDirectory(t *testing.T) {
	tmp := t.TempDir()

	cmd := rootCmd()
	cmd.SetArgs([]string{"init", "myservice"})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	// Run from temp dir so files are created there.
	origDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	err = cmd.Execute()
	require.NoError(t, err)

	// Verify directory was created.
	info, err := os.Stat(filepath.Join(tmp, "myservice"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Verify go.mod was created.
	goMod, err := os.ReadFile(filepath.Join(tmp, "myservice", "go.mod"))
	require.NoError(t, err)
	assert.Contains(t, string(goMod), "module")
	assert.Contains(t, string(goMod), "myservice")

	// Verify output.
	assert.Contains(t, buf.String(), "Created project myservice")
}

func TestInitCommand_WithFlags(t *testing.T) {
	tmp := t.TempDir()

	cmd := rootCmd()
	cmd.SetArgs([]string{"init", "myservice", "--postgres", "--kafka"})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	origDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	err = cmd.Execute()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "postgres")
	assert.Contains(t, output, "kafka")
	assert.Contains(t, output, "Created project myservice")
}

func TestInitCommand_UnknownFlag(t *testing.T) {
	cmd := rootCmd()
	cmd.SetArgs([]string{"init", "myservice", "--unknown-flag"})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	err := cmd.Execute()
	assert.Error(t, err)
}

func TestMigrateCreate_CreatesFiles(t *testing.T) {
	tmp := t.TempDir()
	migrDir := filepath.Join(tmp, "migrations")

	cmd := rootCmd()
	cmd.SetArgs([]string{"migrate", "create", "add_users_table", "--dir", migrDir})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	err := cmd.Execute()
	require.NoError(t, err)

	// Read directory entries.
	entries, err := os.ReadDir(migrDir)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}

	// Check that both files contain the migration name.
	hasUp := false
	hasDown := false
	for _, n := range names {
		if strings.Contains(n, "add_users_table") && strings.HasSuffix(n, ".up.sql") {
			hasUp = true
		}
		if strings.Contains(n, "add_users_table") && strings.HasSuffix(n, ".down.sql") {
			hasDown = true
		}
	}
	assert.True(t, hasUp, "expected .up.sql file")
	assert.True(t, hasDown, "expected .down.sql file")
}

func TestRootCommand_Help(t *testing.T) {
	cmd := rootCmd()
	cmd.SetArgs([]string{})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	err := cmd.Execute()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "goplatform")
	assert.Contains(t, output, "init")
	assert.Contains(t, output, "run")
	assert.Contains(t, output, "migrate")
}
