package ufs

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

type removeAllErrorFS struct{ unlinkErrs map[int]error }

func (fs removeAllErrorFS) Open(string) (File, error)                 { panic("unused") }
func (fs removeAllErrorFS) Remove(string) error                       { panic("unused") }
func (fs removeAllErrorFS) unlinkat(_ int, _ string, flags int) error { return fs.unlinkErrs[flags] }

func TestRemoveAllFromFstatErrorsAndNonDirectory(t *testing.T) {
	oldFstatat := removeAllFstatat
	oldRemoveContents := removeAllContentsFrom
	t.Cleanup(func() {
		removeAllFstatat = oldFstatat
		removeAllContentsFrom = oldRemoveContents
	})
	parent, err := os.Open(t.TempDir())
	require.NoError(t, err)
	defer parent.Close()

	t.Run("entry disappears before stat", func(t *testing.T) {
		removeAllFstatat = func(int, string, *unix.Stat_t, int) error { return ErrNotExist }
		err := removeAllFrom(removeAllErrorFS{unlinkErrs: map[int]error{0: unix.EPERM}}, parent, "vanished")
		require.NoError(t, err)
	})

	t.Run("stat fails", func(t *testing.T) {
		want := errors.New("stat failed")
		removeAllFstatat = func(int, string, *unix.Stat_t, int) error { return want }
		err := removeAllFrom(removeAllErrorFS{unlinkErrs: map[int]error{0: unix.EPERM}}, parent, "entry")
		require.ErrorIs(t, err, want)
		var pathErr *PathError
		require.ErrorAs(t, err, &pathErr)
		require.Equal(t, "fstatat", pathErr.Op)
	})

	t.Run("unlinked entry is not a directory", func(t *testing.T) {
		removeAllFstatat = func(_ int, _ string, st *unix.Stat_t, _ int) error {
			st.Mode = unix.S_IFREG
			return nil
		}
		err := removeAllFrom(removeAllErrorFS{unlinkErrs: map[int]error{0: unix.EPERM}}, parent, "file")
		require.ErrorIs(t, err, unix.EPERM)
		var pathErr *PathError
		require.ErrorAs(t, err, &pathErr)
		require.Equal(t, "unlinkat", pathErr.Op)
	})

	t.Run("directory unlink fails after contents are removed", func(t *testing.T) {
		removeAllContentsFrom = func(unixFS, File, string) error { return nil }
		removeAllFstatat = func(_ int, _ string, st *unix.Stat_t, _ int) error {
			st.Mode = unix.S_IFDIR
			return nil
		}
		fs := removeAllErrorFS{unlinkErrs: map[int]error{0: unix.EISDIR, AT_REMOVEDIR: unix.EACCES}}
		err := removeAllFrom(fs, parent, "dir")
		require.ErrorIs(t, err, unix.EACCES)
		var pathErr *PathError
		require.ErrorAs(t, err, &pathErr)
		require.Equal(t, "unlinkat", pathErr.Op)
	})
}
