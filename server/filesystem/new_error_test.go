package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewReturnsSetupErrors(t *testing.T) {
	t.Run("root path is not a directory", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
		_, err := New(filepath.Join(file, "child"), 0, nil)
		require.Error(t, err)
	})

	t.Run("root initialization fails", func(t *testing.T) {
		oldOpenRoot := filesystemOpenRoot
		t.Cleanup(func() { filesystemOpenRoot = oldOpenRoot })
		want := errors.New("initialize root")
		filesystemOpenRoot = func(string) (*os.Root, error) {
			return nil, want
		}
		_, err := New(t.TempDir(), 0, nil)
		require.ErrorIs(t, err, want)
	})
}
