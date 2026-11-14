package ufs

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

type removeAllErrorFS struct {
	unlinkErrs map[int]error
	openFile   File
	openErr    error
	removeErr  error
}

func (fs removeAllErrorFS) Open(string) (File, error)                 { return fs.openFile, fs.openErr }
func (fs removeAllErrorFS) Remove(string) error                       { return fs.removeErr }
func (fs removeAllErrorFS) unlinkat(_ int, _ string, flags int) error { return fs.unlinkErrs[flags] }

func TestRemoveAllParentErrorsAndPlainRecursiveError(t *testing.T) {
	parent, err := os.Open(t.TempDir())
	require.NoError(t, err)
	defer parent.Close()

	t.Run("missing parent is ignored", func(t *testing.T) {
		fs := removeAllErrorFS{removeErr: unix.EPERM, openErr: ErrNotExist}
		require.NoError(t, removeAll(fs, "missing/entry"))
	})

	t.Run("parent open error is returned", func(t *testing.T) {
		want := errors.New("parent open failed")
		fs := removeAllErrorFS{removeErr: unix.EPERM, openErr: want}
		require.ErrorIs(t, removeAll(fs, "parent/entry"), want)
	})

	t.Run("plain recursive error gains operation and path", func(t *testing.T) {
		oldFstatat := removeAllFstatat
		oldRemoveContents := removeAllContentsFrom
		t.Cleanup(func() {
			removeAllFstatat = oldFstatat
			removeAllContentsFrom = oldRemoveContents
		})
		removeAllFstatat = func(_ int, _ string, st *unix.Stat_t, _ int) error {
			st.Mode = unix.S_IFDIR
			return nil
		}
		want := errors.New("recursive failure")
		removeAllContentsFrom = func(unixFS, File, string) error { return want }
		fs := removeAllErrorFS{
			removeErr: unix.EPERM,
			openFile:  parent,
			unlinkErrs: map[int]error{
				0:            unix.EISDIR,
				AT_REMOVEDIR: unix.EACCES,
			},
		}
		err := removeAll(fs, "parent/entry")
		require.ErrorIs(t, err, want)
		var pathErr *PathError
		require.ErrorAs(t, err, &pathErr)
		require.Equal(t, "removeallfrom", pathErr.Op)
		require.Equal(t, "entry", pathErr.Path)
	})
}

func TestRemoveContentsParentAndWrappingErrors(t *testing.T) {
	parent, err := os.Open(t.TempDir())
	require.NoError(t, err)
	defer parent.Close()

	t.Run("parent open error is returned", func(t *testing.T) {
		want := errors.New("parent open failed")
		require.ErrorIs(t, removeContents(removeAllErrorFS{openErr: want}, "parent/entry"), want)
	})

	t.Run("plain recursive error gains operation and path", func(t *testing.T) {
		old := removeContentsFromDirectory
		t.Cleanup(func() { removeContentsFromDirectory = old })
		want := errors.New("recursive failure")
		removeContentsFromDirectory = func(unixFS, File, string) error { return want }
		err := removeContents(removeAllErrorFS{openFile: parent}, "parent/entry")
		require.ErrorIs(t, err, want)
		var pathErr *PathError
		require.ErrorAs(t, err, &pathErr)
		require.Equal(t, "removecontentsfrom", pathErr.Op)
		require.Equal(t, "entry", pathErr.Path)
	})
}

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
