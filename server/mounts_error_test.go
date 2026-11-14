package server

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveMountPathReturnsSymlinkResolutionErrors(t *testing.T) {
	old := mountPathEvalSymlinks
	t.Cleanup(func() { mountPathEvalSymlinks = old })
	want := errors.New("symlink lookup failed")
	calls := 0
	mountPathEvalSymlinks = func(path string) (string, error) {
		calls++
		if calls == 1 {
			return "", fs.ErrNotExist
		}
		require.Equal(t, filepath.Join(string(filepath.Separator), "a", "b"), path)
		return "", want
	}
	_, err := resolveMountPath(filepath.Join(string(filepath.Separator), "a", "b", "c"), 1000)
	require.ErrorIs(t, err, want)
	require.Equal(t, 2, calls)
}

type mountPathTestInfo struct{ os.FileInfo }

func (mountPathTestInfo) IsDir() bool       { return true }
func (mountPathTestInfo) Mode() os.FileMode { return os.ModeDir | 0o755 }
func (mountPathTestInfo) Sys() any          { return "unknown stat type" }

func TestCheckMountPathParentsLstatErrors(t *testing.T) {
	oldRoot, oldLstat := mountPathCheckRoot, mountPathLstat
	t.Cleanup(func() {
		mountPathCheckRoot = oldRoot
		mountPathLstat = oldLstat
	})
	root := t.TempDir()
	mountPathCheckRoot = root
	want := errors.New("lstat failed")
	mountPathLstat = func(string) (os.FileInfo, error) { return nil, want }
	require.ErrorIs(t, checkMountPathParents(filepath.Join(root, "child"), 1000), want)
}

func TestCheckMountPathParentsRejectsUnknownStatType(t *testing.T) {
	oldRoot, oldLstat := mountPathCheckRoot, mountPathLstat
	t.Cleanup(func() {
		mountPathCheckRoot = oldRoot
		mountPathLstat = oldLstat
	})
	root := t.TempDir()
	mountPathCheckRoot = root
	mountPathLstat = func(string) (os.FileInfo, error) { return mountPathTestInfo{}, nil }
	require.ErrorContains(t, checkMountPathParents(filepath.Join(root, "child"), 1000), "unable to determine owner")
}
