//go:build unix

package ufs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirentAndStatMethods(t *testing.T) {
	fs, err := newTestUnixFS()
	require.NoError(t, err)
	defer fs.Cleanup()

	// Create a test file
	testFile := filepath.Join(fs.Root, "sample.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("test content"), 0644))

	// ReadDir returns []DirEntry
	entries, err := fs.ReadDir("/")
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	var found bool
	for _, entry := range entries {
		if entry.Name() == "sample.txt" {
			found = true
			assert.False(t, entry.IsDir())
			assert.Equal(t, "sample.txt", entry.Name())
			assert.False(t, entry.Type().IsDir())
		}
	}
	assert.True(t, found, "sample.txt was not found in entries")

	// Test FileInfo methods
	info, err := fs.Lstat("sample.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(12), info.Size())
	assert.False(t, info.IsDir())
	assert.Equal(t, "sample.txt", info.Name())
	assert.False(t, info.ModTime().IsZero())
	assert.NotNil(t, info.Sys())
}

