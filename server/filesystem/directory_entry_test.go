package filesystem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatDirectoryEntryUsesRootedMetadataAndMIME(t *testing.T) {
	root := t.TempDir()
	fs, err := New(root, 0, nil)
	require.NoError(t, err)
	defer fs.Close()
	require.NoError(t, os.WriteFile(filepath.Join(root, "entry.txt"), []byte("plain text"), 0o644))

	entries, err := fs.ReadDirStat(".")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	stat := fs.statDirectoryEntry(".", entries[0])
	require.Equal(t, "entry.txt", stat.Name())
	require.Contains(t, stat.Mimetype, "text/plain")
}
