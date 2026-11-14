package ufs

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type removeContentsTestFile struct {
	File
	readErr error
	closed  bool
}

func (f *removeContentsTestFile) Readdirnames(int) ([]string, error) {
	return nil, f.readErr
}

func (f *removeContentsTestFile) Close() error {
	f.closed = true
	return nil
}

func TestRemoveContentsFromOpenAndReadErrors(t *testing.T) {
	parent, err := os.Open(t.TempDir())
	require.NoError(t, err)
	defer parent.Close()

	oldOpen := openRemoveContentsDir
	t.Cleanup(func() { openRemoveContentsDir = oldOpen })

	t.Run("directory disappeared before open", func(t *testing.T) {
		openRemoveContentsDir = func(int, string) (File, error) { return nil, ErrNotExist }
		require.NoError(t, removeContentsFrom(nil, parent, "gone"))
	})

	t.Run("open failure is wrapped", func(t *testing.T) {
		want := errors.New("open failed")
		openRemoveContentsDir = func(int, string) (File, error) { return nil, want }
		err := removeContentsFrom(nil, parent, "dir")
		require.ErrorIs(t, err, want)
		var pathErr *PathError
		require.ErrorAs(t, err, &pathErr)
		require.Equal(t, "openfdat", pathErr.Op)
		require.Equal(t, "dir", pathErr.Path)
	})

	t.Run("directory disappeared during read", func(t *testing.T) {
		file := &removeContentsTestFile{readErr: ErrNotExist}
		openRemoveContentsDir = func(int, string) (File, error) { return file, nil }
		require.NoError(t, removeContentsFrom(nil, parent, "gone"))
		require.True(t, file.closed)
	})

	t.Run("read failure is wrapped", func(t *testing.T) {
		want := errors.New("read failed")
		file := &removeContentsTestFile{readErr: want}
		openRemoveContentsDir = func(int, string) (File, error) { return file, nil }
		err := removeContentsFrom(nil, parent, "dir")
		require.ErrorIs(t, err, want)
		var pathErr *PathError
		require.ErrorAs(t, err, &pathErr)
		require.Equal(t, "readdirnames", pathErr.Op)
		require.Equal(t, "dir", pathErr.Path)
		require.True(t, file.closed)
	})

	t.Run("empty directory reaches EOF", func(t *testing.T) {
		file := &removeContentsTestFile{readErr: io.EOF}
		openRemoveContentsDir = func(int, string) (File, error) { return file, nil }
		require.NoError(t, removeContentsFrom(nil, parent, "empty"))
		require.True(t, file.closed)
	})
}
