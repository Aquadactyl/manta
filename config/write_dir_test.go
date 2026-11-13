package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The directory of the configuration file is created when it is missing.
func TestWriteToDiskCreatesTheConfigurationDirectory(t *testing.T) {
	c, err := NewAtPath(filepath.Join(t.TempDir(), "a", "b", "config.yml"))
	require.NoError(t, err)

	require.NoError(t, WriteToDisk(c))

	st, err := os.Stat(c.path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	dir, err := os.Stat(filepath.Dir(c.path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dir.Mode().Perm())
}

func TestWriteToDiskReportsWhenDestinationIsDirectory(t *testing.T) {
	c, err := NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(c.path, 0o700))
	require.Error(t, WriteToDisk(c))
}
