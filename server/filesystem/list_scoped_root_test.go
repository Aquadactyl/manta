package filesystem

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestNestedListingKeepsDirectoryAndFileSymlinkSemantics(t *testing.T) {
	fs := newListingTestFS(t)
	t.Cleanup(func() { require.NoError(t, fs.Close()) })
	dir := filepath.Join(fs.Path(), "plugins", "config")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entry.txt"), []byte("original text"), 0o600))
	require.NoError(t, os.Symlink("entry.txt", filepath.Join(dir, "link")))
	require.NoError(t, os.Symlink("plugins/config", filepath.Join(fs.Path(), "linked-directory")))
	for _, p := range []string{"plugins/config", "/plugins/config", "linked-directory"} {
		entries, err := fs.ListDirectory(p)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		require.Equal(t, "entry.txt", entries[0].Name())
		require.Contains(t, entries[0].Mimetype, "text/plain")
		require.True(t, entries[1].ToAPIResponse().Symlink)
		require.Equal(t, "application/octet-stream", entries[1].Mimetype)
	}
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(fs.Path(), "outside-directory")))
	_, err := fs.ListDirectory("outside-directory")
	require.Error(t, err)
	_, err = fs.ListDirectory("../")
	require.Error(t, err)
	require.NoError(t, unix.Mkfifo(filepath.Join(fs.Path(), "pipe"), 0o600))
	done := make(chan error, 1)
	go func() { _, err := fs.ListDirectory("pipe"); done <- err }()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("opening a directory capability blocked on a named pipe")
	}
}

func TestScopedListingKeepsOriginalDirectoryWhenParentPathIsReplaced(t *testing.T) {
	fs := newListingTestFS(t)
	t.Cleanup(func() { require.NoError(t, fs.Close()) })
	dir := filepath.Join(fs.Path(), "plugins")
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entry.txt"), []byte("original text"), 0o600))
	root, err := fs.openDirectory("plugins")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	entries, err := directoryInfo(root)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "entry.txt"), append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...), 0o600))
	require.NoError(t, os.Rename(dir, dir+"-original"))
	require.NoError(t, os.Symlink(outside, dir))
	st := statDirectoryEntry(root, entries[0])
	require.True(t, os.SameFile(entries[0], st.FileInfo))
	require.Contains(t, st.Mimetype, "text/plain")
	// A new lookup through the replaced path must still reject the outside link.
	_, err = fs.ListDirectory("plugins")
	require.Error(t, err)
}

func TestDirectoryEntryOpenerRejectsPathsAndClosedDescriptors(t *testing.T) {
	fs := newListingTestFS(t)
	t.Cleanup(func() { require.NoError(t, fs.Close()) })
	directory, err := fs.openDirectory(".")
	require.NoError(t, err)
	for _, name := range []string{"", ".", "..", "../entry", "nested/entry", "/entry", "entry\x00suffix"} {
		file, err := openDirectoryEntry(directory, name)
		require.Nil(t, file)
		require.ErrorIs(t, err, os.ErrInvalid)
	}
	require.NoError(t, directory.Close())
	file, err := openDirectoryEntry(directory, "entry")
	require.Nil(t, file)
	require.Error(t, err)
}
