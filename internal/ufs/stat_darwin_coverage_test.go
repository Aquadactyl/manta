// SPDX-License-Identifier: MIT
//go:build darwin

package ufs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestDarwinStatResolvedPermissionFallback(t *testing.T) {
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	require.NoError(t, os.WriteFile(locked, []byte("data"), 0o600))
	require.NoError(t, os.Chmod(locked, 0))
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	parent, err := os.Open(root)
	require.NoError(t, err)
	defer parent.Close()

	old := userspaceOpenRetry
	t.Cleanup(func() { userspaceOpenRetry = old })

	t.Run("describes unreadable regular files", func(t *testing.T) {
		userspaceOpenRetry = func(dirfd int, name string, flags int, mode uint32) (int, error) {
			if name == "locked" {
				return -1, unix.EACCES
			}
			return old(dirfd, name, flags, mode)
		}
		var st unix.Stat_t
		require.NoError(t, fs.statResolved(int(parent.Fd()), "locked", "locked", &st))
		assert.Equal(t, uint64(unix.S_IFREG), uint64(st.Mode&unix.S_IFMT))
	})

	t.Run("returns the permission error when the parent cannot be opened", func(t *testing.T) {
		userspaceOpenRetry = func(dirfd int, name string, flags int, mode uint32) (int, error) {
			if name == "locked" {
				return -1, unix.EACCES
			}
			if name == "." {
				return -1, unix.EIO
			}
			return old(dirfd, name, flags, mode)
		}
		var st unix.Stat_t
		assert.ErrorIs(t, fs.statResolved(int(parent.Fd()), "locked", "locked", &st), ErrPermission)
	})

	t.Run("returns the permission error for missing and symlink entries", func(t *testing.T) {
		require.NoError(t, os.Symlink("locked", filepath.Join(root, "link")))
		userspaceOpenRetry = func(dirfd int, name string, flags int, mode uint32) (int, error) {
			if name == "missing" || name == "link" {
				return -1, unix.EACCES
			}
			return old(dirfd, name, flags, mode)
		}
		for _, name := range []string{"missing", "link"} {
			var st unix.Stat_t
			assert.ErrorIs(t, fs.statResolved(int(parent.Fd()), name, name, &st), ErrPermission, name)
		}
	})
}
