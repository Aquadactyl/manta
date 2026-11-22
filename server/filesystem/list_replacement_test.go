package filesystem

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestListingReplacedRegularFile(t *testing.T) {
	for _, kind := range []string{"directory", "internal_symlink", "external_symlink", "pipe", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			fs := newListingTestFS(t)
			t.Cleanup(func() { require.NoError(t, fs.Close()) })
			p := filepath.Join(fs.Path(), "entry")
			require.NoError(t, os.WriteFile(p, []byte("old content"), 0o600))
			entries, err := fs.ReadDirStat(".")
			require.NoError(t, err)
			require.Len(t, entries, 1)
			if kind == "internal_symlink" {
				// Allocate the target before unlinking entry so this test cannot
				// accidentally reuse entry's inode on a fast temporary filesystem.
				require.NoError(t, os.WriteFile(filepath.Join(fs.Path(), "target"), []byte("new content"), 0o600))
			}
			require.NoError(t, os.Remove(p))
			switch kind {
			case "directory":
				require.NoError(t, os.Mkdir(p, 0o700))
			case "internal_symlink":
				require.NoError(t, os.Symlink("target", p))
			case "external_symlink":
				outside := filepath.Join(t.TempDir(), "private")
				require.NoError(t, os.WriteFile(outside, []byte("outside content"), 0o600))
				require.NoError(t, os.Symlink(outside, p))
			case "pipe":
				require.NoError(t, unix.Mkfifo(p, 0o600))
			}
			done := make(chan Stat, 1)
			go func() {
				done <- fs.statDirectoryEntry(".", entries[0])
			}()
			select {
			case st := <-done:
				// Metadata is the directory snapshot; do not sniff a replacement
				// inode or follow it into content outside that snapshot.
				require.True(t, os.SameFile(entries[0], st.FileInfo))
				require.Equal(t, "application/octet-stream", st.Mimetype)
			case <-time.After(2 * time.Second):
				t.Fatal("listing blocked on a replaced file")
			}
		})
	}
}
