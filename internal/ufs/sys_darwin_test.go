//go:build darwin

package ufs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The base path is resolved to the path the kernel reports for descriptors
// beneath it, which differs when it contains symbolic links. /var is one on
// macOS, and so is the temporary directory of every test.
func TestDarwinRealBaseResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	resolved, err := filepath.EvalSymlinks(real)
	require.NoError(t, err)

	link := filepath.Join(t.TempDir(), "base")
	require.NoError(t, os.Symlink(real, link))

	fs, err := NewUnixFS(link, false)
	require.NoError(t, err)

	got, err := fs.realBase()
	require.NoError(t, err)
	assert.Equal(t, resolved, got)

	// Files are reachable and reported beneath it, so are not mistaken for an
	// escape.
	require.NoError(t, os.WriteFile(filepath.Join(real, "file.txt"), []byte("data"), 0o644))
	f, err := fs.Open("file.txt")
	require.NoError(t, err)
	_ = f.Close()
}

func TestDarwinFdPath(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	require.NoError(t, err)
	defer unix.Close(fd)

	got, err := fdPath(fd)
	require.NoError(t, err)
	assert.Equal(t, resolved, got)

	_, err = fdPath(-1)
	assert.Error(t, err)
}

func TestDarwinIsPathInside(t *testing.T) {
	for _, tt := range []struct {
		path, base string
		want       bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a/bc", "/a/b", false},
		{"/a", "/a/b", false},
		{"/other/a/b", "/a/b", false},
	} {
		assert.Equal(t, tt.want, isPathInside(tt.path, tt.base), "%s in %s", tt.path, tt.base)
	}
}

// A file that can not be read can still be described, but not through a
// symbolic link, whose target can not be checked without opening it.
func TestDarwinStatUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read every file")
	}

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("data"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dir", "locked.txt"), []byte("data"), 0o000))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "dir", "link")))

	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)

	st, err := fs.Stat("dir/locked.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(4), st.Size())

	_, err = fs.Stat("dir/link")
	assert.Error(t, err)
}

// getdirentries(2) needs a large enough buffer, and returns directories that do
// not fit in one in several reads.
func TestDarwinReadDirLargeDirectory(t *testing.T) {
	root := t.TempDir()
	const total = 2500
	for i := range total {
		require.NoError(t, os.WriteFile(filepath.Join(root, fmt.Sprintf("a-rather-long-file-name-to-fill-the-buffer-%05d.txt", i)), nil, 0o644))
	}

	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)

	entries, err := fs.ReadDir(".")
	require.NoError(t, err)
	assert.Len(t, entries, total)
}

func TestDarwinOpenat2IsNeverEnabled(t *testing.T) {
	fs, err := NewUnixFS(t.TempDir(), true)
	require.NoError(t, err)
	assert.False(t, fs.useOpenat2)
}
