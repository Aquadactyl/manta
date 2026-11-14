package filesystem

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuotaCloseUpdatesUsageFromFileStat(t *testing.T) {
	root := t.TempDir()
	fs, err := New(root, 100, nil)
	require.NoError(t, err)
	defer fs.Close()
	path := root + "/file"
	require.NoError(t, os.WriteFile(path, []byte("data"), 0o600))
	f, err := fs.root.OpenFile("file", os.O_RDWR, 0o600)
	require.NoError(t, err)
	fs.diskUsed.Store(10)
	quota := newQuotaFile(fs, f, 10)
	require.NoError(t, quota.Close())
	require.Equal(t, int64(4), fs.diskUsed.Load())
}
