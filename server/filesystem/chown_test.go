package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/ufs"
)

func TestChownPaths(t *testing.T) {
	previousConfig := config.Get()
	t.Cleanup(func() { config.Set(previousConfig) })
	oldRoot, oldLchown, oldLchownat, oldWalk := filesystemIsRoot, filesystemLchown, filesystemLchownat, filesystemWalkDirat
	t.Cleanup(func() {
		filesystemIsRoot, filesystemLchown, filesystemLchownat, filesystemWalkDirat = oldRoot, oldLchown, oldLchownat, oldWalk
	})
	filesystemIsRoot = func() bool { return true }
	config.Set(&config.Configuration{System: config.SystemConfiguration{}})

	root := t.TempDir()
	fs, err := New(root, 1<<20, nil)
	require.NoError(t, err)
	fs.SetTest(false)
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("contents"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "directory"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "directory", "child"), []byte("child"), 0o600))

	t.Run("non-root is a no-op", func(t *testing.T) {
		filesystemIsRoot = func() bool { return false }
		require.NoError(t, fs.Chown("file"))
		filesystemIsRoot = func() bool { return true }
	})

	t.Run("unsafe path", func(t *testing.T) {
		require.Error(t, fs.Chown("../outside"))
	})

	t.Run("path ownership error", func(t *testing.T) {
		want := errors.New("lchown failed")
		filesystemLchownat = func(*ufs.UnixFS, int, string, int, int) error { return want }
		require.ErrorIs(t, fs.Chown("file"), want)
		filesystemLchownat = oldLchownat
	})

	t.Run("regular file", func(t *testing.T) {
		require.NoError(t, fs.Chown("file"))
	})

	t.Run("walk callback error", func(t *testing.T) {
		want := errors.New("walk entry failed")
		filesystemWalkDirat = func(_ *ufs.UnixFS, dirfd int, name string, walk func(int, string, string, ufs.DirEntry, error) error) error {
			return walk(dirfd, name, name, nil, want)
		}
		err := fs.Chown("directory")
		require.ErrorIs(t, err, want)
		filesystemWalkDirat = oldWalk
	})

	t.Run("walk ownership error", func(t *testing.T) {
		want := errors.New("child lchown failed")
		calls := 0
		filesystemLchownat = func(*ufs.UnixFS, int, string, int, int) error {
			calls++
			if calls == 2 {
				return want
			}
			return nil
		}
		filesystemWalkDirat = func(_ *ufs.UnixFS, dirfd int, name string, walk func(int, string, string, ufs.DirEntry, error) error) error {
			return walk(dirfd, "child", "child", nil, nil)
		}
		err := fs.Chown("directory")
		require.ErrorIs(t, err, want)
		filesystemLchownat, filesystemWalkDirat = oldLchownat, oldWalk
	})

	t.Run("directory walk succeeds", func(t *testing.T) {
		cfg := config.Get()
		cfg.System.User.Uid, cfg.System.User.Gid = os.Geteuid(), os.Getegid()
		require.NoError(t, fs.Chown("directory"))
	})
}
