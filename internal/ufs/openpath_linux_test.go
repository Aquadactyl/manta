// SPDX-License-Identifier: MIT
//go:build linux

package ufs

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestOpenedPathIsSymlinkBranches(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink("file", link))

	fd, err := unix.Open(link, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	require.NoError(t, err)
	defer unix.Close(fd)
	assert.True(t, openedPathIsSymlink(fd, oPath, false))
	assert.False(t, openedPathIsSymlink(fd, oPath, true))
	assert.False(t, openedPathIsSymlink(fd, unix.O_RDONLY, false))

	old := statOpenedPath
	statOpenedPath = func(int, *unix.Stat_t) error { return syscall.EBADF }
	t.Cleanup(func() { statOpenedPath = old })
	assert.False(t, openedPathIsSymlink(fd, oPath, false))
}
