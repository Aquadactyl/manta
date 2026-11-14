package filesystem

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/internal/ufs"
)

type statTestFile struct {
	ufs.File
	info    ufs.FileInfo
	statErr error
	readErr error
	seekErr error
	closed  *bool
}

func (f statTestFile) Stat() (ufs.FileInfo, error) { return f.info, f.statErr }
func (f statTestFile) Read([]byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	return 0, io.EOF
}
func (f statTestFile) Seek(int64, int) (int64, error) { return 0, f.seekErr }
func (f statTestFile) Close() error {
	if f.closed != nil {
		*f.closed = true
	}
	return nil
}

func TestStatFromFileErrorAndDirectoryPaths(t *testing.T) {
	t.Run("stat error", func(t *testing.T) {
		want := errors.New("stat failed")
		_, err := statFromFile(statTestFile{statErr: want})
		require.ErrorIs(t, err, want)
	})

	t.Run("MIME read error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(path, []byte("data"), 0o600))
		info, err := os.Stat(path)
		require.NoError(t, err)
		want := errors.New("read failed")
		_, err = statFromFile(statTestFile{info: info, readErr: want})
		require.ErrorIs(t, err, want)
	})

	t.Run("seek error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(path, []byte("data"), 0o600))
		info, err := os.Stat(path)
		require.NoError(t, err)
		want := errors.New("seek failed")
		_, err = statFromFile(statTestFile{info: info, seekErr: want})
		require.ErrorIs(t, err, want)
	})

	t.Run("directory skips MIME probe", func(t *testing.T) {
		path := t.TempDir()
		info, err := os.Stat(path)
		require.NoError(t, err)
		st, err := statFromFile(statTestFile{info: info, readErr: errors.New("must not read")})
		require.NoError(t, err)
		require.Equal(t, "inode/directory", st.Mimetype)
	})
}

func TestFilesystemFileClosesWhenStatFails(t *testing.T) {
	fs, err := New(t.TempDir(), 0, nil)
	require.NoError(t, err)
	want := errors.New("stat failed")
	closed := false
	oldOpen := filesystemOpen
	t.Cleanup(func() { filesystemOpen = oldOpen })
	filesystemOpen = func(*ufs.Quota, string) (ufs.File, error) {
		return statTestFile{statErr: want, closed: &closed}, nil
	}

	file, _, err := fs.File("file")
	require.ErrorIs(t, err, want)
	require.Nil(t, file)
	require.True(t, closed)
}

func TestFilesystemStatClosesWhenFileStatFails(t *testing.T) {
	fs, err := New(t.TempDir(), 0, nil)
	require.NoError(t, err)
	want := errors.New("stat failed")
	closed := false
	oldOpen := filesystemOpen
	t.Cleanup(func() { filesystemOpen = oldOpen })
	filesystemOpen = func(*ufs.Quota, string) (ufs.File, error) {
		return statTestFile{statErr: want, closed: &closed}, nil
	}

	_, err = fs.Stat("file")
	require.ErrorIs(t, err, want)
	require.True(t, closed)
}
