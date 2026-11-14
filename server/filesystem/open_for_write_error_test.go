package filesystem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenForWriteUsesRootedFileAndSize(t *testing.T) {
	root := t.TempDir()
	fs, err := New(root, 0, nil)
	require.NoError(t, err)
	defer fs.Close()

	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("12345"), 0o600))
	f, size, err := fs.openForWrite("file", os.O_RDWR|os.O_TRUNC, 0o600)
	require.NoError(t, err)
	require.Equal(t, int64(5), size)
	info, err := f.Stat()
	require.NoError(t, err)
	require.Zero(t, info.Size())
	require.NoError(t, f.Close())

	f, size, err = fs.openForWrite("nested/new", os.O_RDWR, 0o600)
	require.NoError(t, err)
	require.Zero(t, size)
	require.NoError(t, f.Close())

	require.NoError(t, os.WriteFile(filepath.Join(root, "parent"), []byte("x"), 0o600))
	_, _, err = fs.openForWrite("parent/child", os.O_RDWR, 0o600)
	require.Error(t, err)
}
