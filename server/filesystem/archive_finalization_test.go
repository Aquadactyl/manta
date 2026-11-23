package filesystem

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

type archiveFailAfterWriter struct{ remaining int }

func (w *archiveFailAfterWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		n := w.remaining
		w.remaining = 0
		return n, io.ErrClosedPipe
	}
	w.remaining -= len(p)
	return len(p), nil
}

func TestArchiveReportsFinalFlushErrors(t *testing.T) {
	fs := newListingTestFS(t)
	t.Cleanup(func() { require.NoError(t, fs.Close()) })
	for _, budget := range []int{0, 10, 14} {
		a := &Archive{Filesystem: fs}
		require.ErrorIs(t, a.Stream(context.Background(), &archiveFailAfterWriter{remaining: budget}), io.ErrClosedPipe)
	}
	a := &Archive{Filesystem: fs, BaseDirectory: "missing"}
	require.ErrorIs(t, a.Stream(context.Background(), &archiveFailAfterWriter{}), os.ErrNotExist, "flush errors must not replace the original failure")
}

func TestSelectedArchivePathsKeepLiteralBoundariesAndCompleteFooters(t *testing.T) {
	fs := newListingTestFS(t)
	t.Cleanup(func() { require.NoError(t, fs.Close()) })
	for _, name := range []string{"world/level.dat", "world/region/chunk", "world-backup/level.dat", "literal[1]/file", "literal1/file", "server.properties"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(fs.Path(), name)), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(fs.Path(), name), []byte(name), 0o600))
	}
	for _, test := range []struct {
		name           string
		selected, want []string
	}{
		{"directory", []string{"world"}, []string{"world/level.dat", "world/region/chunk"}},
		{"trailing slash and duplicate", []string{"world/", "world/"}, []string{"world/level.dat", "world/region/chunk"}},
		{"exact file", []string{"world/level.dat"}, []string{"world/level.dat"}},
		{"literal punctuation", []string{"literal[1]"}, []string{"literal[1]/file"}},
		{"missing and empty", []string{"", "/", "./world", "world//", "no-such-file"}, nil},
		{"absolute source path", []string{filepath.Join(fs.Path(), "server.properties")}, []string{"server.properties"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			a := &Archive{Filesystem: fs, Files: test.selected}
			require.NoError(t, a.Stream(context.Background(), &out))
			gz, err := gzip.NewReader(bytes.NewReader(out.Bytes()))
			require.NoError(t, err)
			defer gz.Close()
			tr := tar.NewReader(gz)
			var names []string
			for {
				header, err := tr.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(t, err)
				contents, err := io.ReadAll(tr)
				require.NoError(t, err)
				require.Equal(t, header.Name, string(contents))
				names = append(names, header.Name)
			}
			_, err = io.Copy(io.Discard, gz)
			require.NoError(t, err, "gzip checksum and footer must be valid")
			sort.Strings(names)
			require.Equal(t, test.want, names)
		})
	}
}
