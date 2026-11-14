package filesystem

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Aquadactyl/manta/config"
)

func TestChownPaths(t *testing.T) {
	oldRoot, oldLchown := filesystemIsRoot, filesystemLchown
	t.Cleanup(func() { filesystemIsRoot, filesystemLchown = oldRoot, oldLchown })
	filesystemIsRoot = func() bool { return true }
	filesystemLchown = func(*os.Root, string, int, int) error { return nil }
	previousConfig := config.Get()
	t.Cleanup(func() { config.Set(previousConfig) })
	cfg := &config.Configuration{}
	cfg.System.User.UID, cfg.System.User.GID = os.Geteuid(), os.Getegid()
	config.Set(cfg)

	root := t.TempDir()
	fs, err := New(root, 1<<20, nil)
	require.NoError(t, err)
	defer fs.Close()
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
		filesystemLchown = oldLchown
		require.Error(t, fs.Chown("../outside"))
		filesystemLchown = func(*os.Root, string, int, int) error { return nil }
	})
	t.Run("path ownership error", func(t *testing.T) {
		want := errors.New("lchown failed")
		filesystemLchown = func(*os.Root, string, int, int) error { return want }
		require.ErrorIs(t, fs.Chown("file"), want)
		filesystemLchown = func(*os.Root, string, int, int) error { return nil }
	})
	require.NoError(t, fs.Chown("directory"))
}

func TestWriteOperationsReturnOwnershipErrors(t *testing.T) {
	oldRoot, oldLchown := filesystemIsRoot, filesystemLchown
	t.Cleanup(func() { filesystemIsRoot, filesystemLchown = oldRoot, oldLchown })
	filesystemIsRoot = func() bool { return true }
	want := errors.New("ownership update failed")
	filesystemLchown = func(*os.Root, string, int, int) error { return want }
	previousConfig := config.Get()
	config.Set(&config.Configuration{})
	t.Cleanup(func() { config.Set(previousConfig) })

	for _, operation := range []struct {
		name string
		call func(*Filesystem) error
	}{
		{name: "Writefile", call: func(fs *Filesystem) error { return fs.Writefile("file", bytes.NewReader([]byte("data"))) }},
		{name: "Write", call: func(fs *Filesystem) error { return fs.Write("file", bytes.NewReader([]byte("data")), 4, 0o644) }},
		{name: "WriteFrom", call: func(fs *Filesystem) error {
			_, err := fs.WriteFrom("file", bytes.NewReader([]byte("data")))
			return err
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			fs, err := New(t.TempDir(), 1<<20, nil)
			require.NoError(t, err)
			defer fs.Close()
			fs.SetTest(false)
			require.ErrorIs(t, operation.call(fs), want)
		})
	}
}
