//go:build darwin

package ufs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// Paths are always resolved by UnixFS itself on macOS, whatever was asked for.
func TestDarwinAlwaysResolvesBeneath(t *testing.T) {
	for _, asked := range []bool{false, true} {
		fs, err := NewUnixFS(t.TempDir(), asked)
		require.NoError(t, err)
		assert.True(t, fs.useOpenat2)
	}
}

// A path longer than PATH_MAX is reachable, as nothing needs the whole path at
// once, and a link out of the base is still refused deep inside of it.
func TestDarwinDeepPaths(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(outside, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "sub", "secret.txt"), []byte("secret"), 0o644))

	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)

	// Build a path of about 2 KiB one directory at a time, as it can not be
	// created in one call.
	rel := ""
	for range 40 {
		rel = filepath.Join(rel, strings.Repeat("d", 50))
		require.NoError(t, fs.Mkdir(rel, 0o755))
	}
	deep := filepath.Join(rel, "file.txt")
	f, err := fs.Create(deep)
	require.NoError(t, err)
	_, err = f.Write([]byte("deep"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	f, err = fs.Open(deep)
	require.NoError(t, err)
	b, err := io.ReadAll(f)
	require.NoError(t, err)
	_ = f.Close()
	assert.Equal(t, "deep", string(b))

	// A link out of the base in the middle of the path is a bad resolution.
	require.NoError(t, fs.Symlink(outside, filepath.Join(rel, "escape")))
	_, err = fs.Open(filepath.Join(rel, "escape", "sub", "secret.txt"))
	assert.ErrorIs(t, err, ErrBadPathResolution)

	// As the last directory of the path it is not followed at all.
	_, err = fs.Open(filepath.Join(rel, "escape", "secret.txt"))
	assert.Error(t, err)
}

// The spelling of the base does not matter, which it did when paths were
// compared. Opening it through a link, or through a different case on a volume
// that ignores case, works.
func TestDarwinBaseSpelling(t *testing.T) {
	real := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(real, "file.txt"), []byte("data"), 0o644))

	link := filepath.Join(t.TempDir(), "base")
	require.NoError(t, os.Symlink(real, link))

	fs, err := NewUnixFS(link, false)
	require.NoError(t, err)

	f, err := fs.Open("file.txt")
	require.NoError(t, err)
	_ = f.Close()
}
