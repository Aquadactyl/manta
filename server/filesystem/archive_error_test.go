package filesystem

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddToArchiveReturnsHeaderErrors(t *testing.T) {
	root := t.TempDir()
	fs, err := New(root, 0, nil)
	require.NoError(t, err)
	defer fs.Close()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("contents"), 0o600))

	out := &bytes.Buffer{}
	archive := &Archive{Filesystem: fs, w: NewTarProgress(tar.NewWriter(out), nil)}
	previous := archiveFileInfoHeader
	t.Cleanup(func() { archiveFileInfoHeader = previous })
	want := errors.New("header failed")
	archiveFileInfoHeader = func(os.FileInfo, string) (*tar.Header, error) { return nil, want }
	require.ErrorIs(t, archive.addToArchive("file", "file"), want)
}
