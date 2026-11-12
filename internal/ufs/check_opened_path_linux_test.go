//go:build linux

package ufs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// checkOpenedPath verifies a descriptor opened without openat2 through /proc, so
// it only exists on Linux. macOS resolves paths beneath their directory instead,
// and has nothing left to verify afterwards.
func TestCheckOpenedPath(t *testing.T) {
	root := t.TempDir()
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	defer fs.Close()

	// A file outside of the base is refused, whichever way paths are resolved.
	hostsFd, err := unix.Open("/etc/hosts", unix.O_RDONLY, 0)
	require.NoError(t, err)
	defer unix.Close(hostsFd)

	assert.ErrorIs(t, fs.checkOpenedPath(hostsFd, "hosts"), ErrBadPathResolution)
	fs.useOpenat2 = true
	assert.ErrorIs(t, fs.checkOpenedPath(hostsFd, "hosts"), ErrBadPathResolution)
	fs.useOpenat2 = false

	outsideFile, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	defer outsideFile.Close()
	assert.ErrorIs(t, fs.checkOpenedPath(int(outsideFile.Fd()), "out"), ErrBadPathResolution)

	// A deleted file outside of the base is refused, one inside is reported as
	// missing.
	delOut, err := os.CreateTemp(t.TempDir(), "del_out")
	require.NoError(t, err)
	defer delOut.Close()
	_ = os.Remove(delOut.Name())
	assert.ErrorIs(t, fs.checkOpenedPath(int(delOut.Fd()), "del_out"), ErrBadPathResolution)

	delIn, err := os.CreateTemp(fs.BasePath(), "del_in")
	require.NoError(t, err)
	defer delIn.Close()
	_ = os.Remove(delIn.Name())
	assert.ErrorIs(t, fs.checkOpenedPath(int(delIn.Fd()), "del_in"), ErrNotExist)
}
