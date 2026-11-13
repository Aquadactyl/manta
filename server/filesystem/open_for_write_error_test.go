package filesystem

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/internal/ufs"
)

type openForWriteFile struct {
	ufs.File
	info        ufs.FileInfo
	statErr     error
	truncateErr error
	closed      bool
	truncated   bool
}

func (f *openForWriteFile) Stat() (ufs.FileInfo, error) { return f.info, f.statErr }
func (f *openForWriteFile) Truncate(int64) error {
	f.truncated = true
	return f.truncateErr
}
func (f *openForWriteFile) Close() error {
	f.closed = true
	return nil
}

func TestOpenForWriteHandlesFailuresAndNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	fs, err := New(root, 0, nil)
	require.NoError(t, err)

	oldTouch := filesystemTouch
	t.Cleanup(func() { filesystemTouch = oldTouch })

	openErr := errors.New("open failed")
	filesystemTouch = func(*ufs.Quota, string, int, ufs.FileMode) (ufs.File, error) {
		return nil, openErr
	}
	file, size, err := fs.openForWrite("file", ufs.O_RDWR|ufs.O_TRUNC, 0o600)
	require.Nil(t, file)
	require.Zero(t, size)
	require.ErrorIs(t, err, openErr)

	statErr := errors.New("stat failed")
	statFailure := &openForWriteFile{statErr: statErr}
	filesystemTouch = func(*ufs.Quota, string, int, ufs.FileMode) (ufs.File, error) {
		return statFailure, nil
	}
	file, size, err = fs.openForWrite("file", ufs.O_RDWR, 0o600)
	require.Nil(t, file)
	require.Zero(t, size)
	require.ErrorIs(t, err, statErr)
	require.True(t, statFailure.closed)

	truncateErr := errors.New("truncate failed")
	truncateFailure := &openForWriteFile{info: fileInfo(t, root), truncateErr: truncateErr}
	require.True(t, truncateFailure.info.Mode().IsRegular())
	require.Equal(t, int64(5), truncateFailure.info.Size())
	filesystemTouch = func(*ufs.Quota, string, int, ufs.FileMode) (ufs.File, error) {
		return truncateFailure, nil
	}
	file, size, err = fs.openForWrite("file", ufs.O_RDWR|ufs.O_TRUNC, 0o600)
	require.Nil(t, file)
	require.Zero(t, size)
	require.ErrorIs(t, err, truncateErr)
	require.True(t, truncateFailure.truncated)
	require.True(t, truncateFailure.closed)

	truncateSuccess := &openForWriteFile{info: truncateFailure.info}
	filesystemTouch = func(*ufs.Quota, string, int, ufs.FileMode) (ufs.File, error) {
		return truncateSuccess, nil
	}
	file, size, err = fs.openForWrite("file", ufs.O_RDWR|ufs.O_TRUNC, 0o600)
	require.NoError(t, err)
	require.Same(t, truncateSuccess, file)
	require.Equal(t, int64(5), size)
	require.True(t, truncateSuccess.truncated)

	directory, err := os.Stat(root)
	require.NoError(t, err)
	directoryFile := &openForWriteFile{info: directory}
	filesystemTouch = func(*ufs.Quota, string, int, ufs.FileMode) (ufs.File, error) {
		return directoryFile, nil
	}
	file, size, err = fs.openForWrite("directory", ufs.O_RDWR, 0o600)
	require.NoError(t, err)
	require.Same(t, directoryFile, file)
	require.Zero(t, size)
	require.False(t, directoryFile.truncated)
}

func fileInfo(t *testing.T, root string) ufs.FileInfo {
	t.Helper()
	p := root + "/regular-file"
	require.NoError(t, os.WriteFile(p, []byte("12345"), 0o600))
	st, err := os.Stat(p)
	require.NoError(t, err)
	return st
}
