// SPDX-License-Identifier: MIT
//go:build unix

package ufs

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestOpenatResolvedTypeRetriesInterruptedOpen(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(root+"/file", []byte("content"), 0o600))
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	// Darwin defaults to the userspace RESOLVE_BENEATH implementation even when
	// openat2 was not requested; force the openat branch whose EINTR retry is under test.
	fs.useOpenat2 = false
	parent, err := os.Open(root)
	require.NoError(t, err)
	defer parent.Close()

	oldOpenat := ufsOpenat
	t.Cleanup(func() { ufsOpenat = oldOpenat })
	calls := 0
	ufsOpenat = func(dirfd int, name string, flags int, mode uint32) (int, error) {
		calls++
		if calls == 1 {
			return -1, unix.EINTR
		}
		return oldOpenat(dirfd, name, flags, mode)
	}

	fd, err := fs.openatResolvedType(int(parent.Fd()), "file", unix.O_RDONLY, 0, false)
	if runtime.GOOS == "darwin" {
		// Darwin's userspace resolver does not perform post-open path verification.
		// The injected EINTR retry still occurs before that platform-specific result.
		require.ErrorContains(t, err, "path verification after opening is not used on macOS")
		require.Equal(t, 2, calls)
		return
	}
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.NoError(t, unix.Close(fd))
}

func TestOpenatResolvedTypeReturnsDescriptorAfterPathCheck(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(root+"/file", []byte("content"), 0o600))
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	fs.useOpenat2 = false
	parent, err := os.Open(root)
	require.NoError(t, err)
	defer parent.Close()

	old := checkOpenedPathForOpen
	checkOpenedPathForOpen = func(*UnixFS, int, string) error { return nil }
	t.Cleanup(func() { checkOpenedPathForOpen = old })
	fd, err := fs.openatResolvedType(int(parent.Fd()), "file", unix.O_RDONLY, 0, false)
	require.NoError(t, err)
	require.NoError(t, unix.Close(fd))
}

func TestOpenatResolvedTypeClosesFileWhenTypeCheckFails(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(root+"/file", []byte("content"), 0o600))
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	parent, err := os.Open(root)
	require.NoError(t, err)
	defer parent.Close()

	oldSetNonblock := ufsSetNonblock
	t.Cleanup(func() { ufsSetNonblock = oldSetNonblock })
	ufsSetNonblock = func(int, bool) error { return unix.EBADF }

	fd, err := fs.openatResolvedType(int(parent.Fd()), "file", unix.O_RDONLY, 0, true)
	require.ErrorIs(t, err, unix.EBADF)
	require.Zero(t, fd)
}

func TestCheckOpenedTypeReportsSetNonblockError(t *testing.T) {
	f, err := os.Open(t.TempDir())
	require.NoError(t, err)
	defer f.Close()

	oldSetNonblock := ufsSetNonblock
	t.Cleanup(func() { ufsSetNonblock = oldSetNonblock })
	ufsSetNonblock = func(int, bool) error { return unix.EBADF }

	err = checkOpenedType(int(f.Fd()), "dir")
	require.ErrorIs(t, err, unix.EBADF)
	var pathErr *PathError
	require.ErrorAs(t, err, &pathErr)
	require.Equal(t, "fcntl", pathErr.Op)
}
