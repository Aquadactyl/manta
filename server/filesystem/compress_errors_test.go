package filesystem

import (
	"context"
	"errors"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mholt/archives"
	"github.com/pterodactyl/wings/internal/ufs"
	"github.com/stretchr/testify/require"
)

type archiveFileErrors struct {
	ufs.File
	statErr  error
	closeErr error
}

func (f *archiveFileErrors) Stat() (ufs.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return f.File.Stat()
}

func (f *archiveFileErrors) Close() error {
	_ = f.File.Close()
	return f.closeErr
}

func TestCompressFilesStreamAndFileErrors(t *testing.T) {
	t.Run("stream error removes the partial archive", func(t *testing.T) {
		fs := newCompressTestFilesystem(t)
		want := errors.New("stream failed")
		previous := streamArchive
		streamArchive = func(_ *Archive, _ context.Context, _ io.Writer) error { return want }
		t.Cleanup(func() { streamArchive = previous })

		_, err := fs.CompressFiles("/", []string{"source.txt"})
		require.ErrorIs(t, err, want)
		entries, err := fs.ReadDir("/")
		require.NoError(t, err)
		for _, entry := range entries {
			require.NotContains(t, entry.Name(), "archive-")
		}
	})

	t.Run("archive stat error is returned", func(t *testing.T) {
		fs := newCompressTestFilesystem(t)
		want := errors.New("stat failed")
		withArchiveFileErrors(t, want, nil)

		_, err := fs.CompressFiles("/", []string{"source.txt"})
		require.ErrorIs(t, err, want)
	})

	t.Run("archive close error is returned", func(t *testing.T) {
		fs := newCompressTestFilesystem(t)
		want := errors.New("close failed")
		withArchiveFileErrors(t, nil, want)

		_, err := fs.CompressFiles("/", []string{"source.txt"})
		require.ErrorIs(t, err, want)
	})
}

type failingTestDecompressor struct{ err error }

func (f failingTestDecompressor) Extension() string { return ".gz" }
func (f failingTestDecompressor) MediaType() string { return "application/gzip" }
func (f failingTestDecompressor) Match(context.Context, string, io.Reader) (archives.MatchResult, error) {
	return archives.MatchResult{}, nil
}
func (f failingTestDecompressor) OpenReader(io.Reader) (io.ReadCloser, error) {
	return nil, f.err
}

type callbackTestExtractor struct{ entry archives.FileInfo }

func (callbackTestExtractor) Extension() string { return ".test" }
func (callbackTestExtractor) MediaType() string { return "application/x-test" }
func (callbackTestExtractor) Match(context.Context, string, io.Reader) (archives.MatchResult, error) {
	return archives.MatchResult{}, nil
}
func (f callbackTestExtractor) Extract(ctx context.Context, _ io.Reader, handle archives.FileHandler) error {
	return handle(ctx, f.entry)
}

func TestExtractStreamReturnsDecompressorAndEntryErrors(t *testing.T) {
	fs := newCompressTestFilesystem(t)
	want := errors.New("open decompressor failed")
	err := fs.extractStream(context.Background(), extractStreamOptions{
		Directory: "/", FileName: "source.txt.gz", Format: failingTestDecompressor{err: want}, Reader: strings.NewReader("data"),
	})
	require.ErrorIs(t, err, want)

	source := filepath.Join(t.TempDir(), "entry")
	require.NoError(t, os.WriteFile(source, []byte("data"), 0o644))
	info, err := os.Stat(source)
	require.NoError(t, err)

	t.Run("entry open error", func(t *testing.T) {
		want := errors.New("entry open failed")
		format := callbackTestExtractor{entry: archives.FileInfo{
			FileInfo: info, NameInArchive: "open-failure", Open: func() (iofs.File, error) { return nil, want },
		}}
		err := fs.extractStream(context.Background(), extractStreamOptions{Directory: "/", FileName: "archive.test", Format: format})
		require.ErrorIs(t, err, want)
	})

	t.Run("write error", func(t *testing.T) {
		limited, err := New(t.TempDir(), 1, nil)
		require.NoError(t, err)
		limited.SetTest(true)
		limited.unixFS.SetUsage(0)
		t.Cleanup(func() { _ = limited.unixFS.Close() })
		format := callbackTestExtractor{entry: archives.FileInfo{
			FileInfo: info, NameInArchive: "too-large", Open: func() (iofs.File, error) { return os.Open(source) },
		}}
		err = limited.extractStream(context.Background(), extractStreamOptions{Directory: "/", FileName: "archive.test", Format: format})
		require.Error(t, err)
	})

	t.Run("modification time error", func(t *testing.T) {
		old := setArchiveModTime
		setArchiveModTime = func(*Filesystem, string, time.Time) error { return want }
		t.Cleanup(func() { setArchiveModTime = old })
		format := callbackTestExtractor{entry: archives.FileInfo{
			FileInfo: info, NameInArchive: "mtime-failure", Open: func() (iofs.File, error) { return os.Open(source) },
		}}
		err := fs.extractStream(context.Background(), extractStreamOptions{Directory: "/", FileName: "archive.test", Format: format})
		require.ErrorIs(t, err, want)
	})
}

func newCompressTestFilesystem(t *testing.T) *Filesystem {
	t.Helper()
	fs, err := New(t.TempDir(), 0, nil)
	require.NoError(t, err)
	require.NoError(t, fs.Writefile("source.txt", strings.NewReader("source")))
	t.Cleanup(func() { _ = fs.unixFS.Close() })
	return fs
}

func withArchiveFileErrors(t *testing.T, statErr, closeErr error) {
	t.Helper()
	previous := openArchiveFile
	openArchiveFile = func(fs *ufs.Quota, name string, flag int, mode ufs.FileMode) (ufs.File, error) {
		file, err := fs.OpenFile(name, flag, mode)
		if err != nil {
			return nil, err
		}
		return &archiveFileErrors{File: file, statErr: statErr, closeErr: closeErr}, nil
	}
	t.Cleanup(func() { openArchiveFile = previous })
}
