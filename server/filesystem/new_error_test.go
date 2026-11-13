package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/internal/ufs"
)

func TestNewReturnsSetupErrors(t *testing.T) {
	t.Run("root path is not a directory", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
		_, err := New(filepath.Join(file, "child"), 0, nil)
		require.Error(t, err)
	})

	t.Run("UnixFS initialization fails", func(t *testing.T) {
		oldNewUnixFS := filesystemNewUnixFS
		t.Cleanup(func() { filesystemNewUnixFS = oldNewUnixFS })
		want := errors.New("initialize UnixFS")
		filesystemNewUnixFS = func(string, bool) (*ufs.UnixFS, error) {
			return nil, want
		}
		_, err := New(t.TempDir(), 0, nil)
		require.ErrorIs(t, err, want)
	})
}
