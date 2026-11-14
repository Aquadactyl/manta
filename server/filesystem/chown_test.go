package filesystem

import (
	"bytes"
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
	cfg := &config.Configuration{}
	cfg.System.User.Uid, cfg.System.User.Gid = os.Geteuid(), os.Getegid()
	config.Set(cfg)

	root := t.TempDir()
	fs, err := New(root, 1<<20, nil)
	require.NoError(t, err)
	fs.SetTest(false)
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("contents"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "directory"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "directory", "child"), []byte("child"), 0o600))
	require.NoError(t, filesystemLchown(fs.unixFS.UnixFS, "file", os.Geteuid(), os.Getegid()))

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

func TestWriteOperationsReturnOwnershipErrors(t *testing.T) {
	oldRoot, oldLchown, oldLchownat := filesystemIsRoot, filesystemLchown, filesystemLchownat
	t.Cleanup(func() {
		filesystemIsRoot, filesystemLchown, filesystemLchownat = oldRoot, oldLchown, oldLchownat
	})
	filesystemIsRoot = func() bool { return true }
	want := errors.New("ownership update failed")
	filesystemLchown = func(*ufs.UnixFS, string, int, int) error { return want }
	filesystemLchownat = func(*ufs.UnixFS, int, string, int, int) error { return want }
	previousConfig := config.Get()
	config.Set(&config.Configuration{})
	t.Cleanup(func() { config.Set(previousConfig) })

	t.Run("Writefile", func(t *testing.T) {
		root := t.TempDir()
		fs, err := New(root, 1<<20, nil)
		require.NoError(t, err)
		fs.SetTest(false)
		require.ErrorIs(t, fs.Writefile("file", bytes.NewReader([]byte("data"))), want)
	})
	t.Run("Write", func(t *testing.T) {
		root := t.TempDir()
		fs, err := New(root, 1<<20, nil)
		require.NoError(t, err)
		fs.SetTest(false)
		require.ErrorIs(t, fs.Write("file", bytes.NewReader([]byte("data")), 4, 0o644), want)
	})
	t.Run("WriteFrom", func(t *testing.T) {
		root := t.TempDir()
		fs, err := New(root, 1<<20, nil)
		require.NoError(t, err)
		fs.SetTest(false)
		_, err = fs.WriteFrom("file", bytes.NewReader([]byte("data")))
		require.ErrorIs(t, err, want)
		require.NoFileExists(t, filepath.Join(root, "file"))
	})
	t.Run("WriteFrom parent is not a directory", func(t *testing.T) {
		root := t.TempDir()
		fs, err := New(root, 1<<20, nil)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, "parent"), []byte("x"), 0o644))
		_, err = fs.WriteFrom("parent/file", bytes.NewReader(nil))
		require.Error(t, err)
	})
	t.Run("WriteFrom touch error", func(t *testing.T) {
		root := t.TempDir()
		fs, err := New(root, 1<<20, nil)
		require.NoError(t, err)
		oldTouch := filesystemTouch
		filesystemTouch = func(*ufs.Quota, string, int, ufs.FileMode) (ufs.File, error) { return nil, want }
		t.Cleanup(func() { filesystemTouch = oldTouch })
		_, err = fs.WriteFrom("file", bytes.NewReader(nil))
		require.ErrorIs(t, err, want)
	})
	t.Run("Write truncate error", func(t *testing.T) {
		root := t.TempDir()
		fs, err := New(root, 1<<20, nil)
		require.NoError(t, err)
		fake := &openForWriteFile{info: fileInfo(t, root), truncateErr: want}
		oldTouch := filesystemTouch
		filesystemTouch = func(*ufs.Quota, string, int, ufs.FileMode) (ufs.File, error) { return fake, nil }
		t.Cleanup(func() { filesystemTouch = oldTouch })
		fs.SetTest(true)
		err = fs.Write("file", bytes.NewReader([]byte("data")), 4, 0o644)
		require.ErrorIs(t, err, want)
		require.True(t, fake.truncated)
	})
	t.Run("mkdirAll", func(t *testing.T) {
		root := t.TempDir()
		fs, err := New(root, 1<<20, nil)
		require.NoError(t, err)
		fs.SetTest(false)
		require.ErrorIs(t, fs.Write("nested/file", bytes.NewReader([]byte("data")), 4, 0o644), want)
	})
	t.Run("Copy", func(t *testing.T) {
		root := t.TempDir()
		fs, err := New(root, 1<<20, nil)
		require.NoError(t, err)
		fs.SetTest(false)
		require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte("data"), 0o644))
		require.ErrorIs(t, fs.Copy("source"), want)
	})
}

func TestCopyReturnsDestinationCreationErrors(t *testing.T) {
	root := t.TempDir()
	fs, err := New(root, 1<<20, nil)
	require.NoError(t, err)
	fs.SetTest(true)
	require.NoError(t, os.WriteFile(filepath.Join(root, "source.txt"), []byte("data"), 0o644))
	oldOpenFileat := filesystemOpenFileat
	t.Cleanup(func() { filesystemOpenFileat = oldOpenFileat })

	want := errors.New("destination create failed")
	filesystemOpenFileat = func(unixFS *ufs.UnixFS, dirfd int, name string, flag int, mode ufs.FileMode) (ufs.File, error) {
		if flag&ufs.O_WRONLY == 0 {
			return unixFS.OpenFileat(dirfd, name, flag, mode)
		}
		return nil, want
	}
	require.ErrorIs(t, fs.Copy("source.txt"), want)

	attempts := 0
	filesystemOpenFileat = func(unixFS *ufs.UnixFS, dirfd int, name string, flag int, mode ufs.FileMode) (ufs.File, error) {
		if flag&ufs.O_WRONLY == 0 {
			return unixFS.OpenFileat(dirfd, name, flag, mode)
		}
		attempts++
		return nil, ufs.ErrExist
	}
	require.ErrorIs(t, fs.Copy("source.txt"), ufs.ErrExist)
	require.Equal(t, 11, attempts)
}
