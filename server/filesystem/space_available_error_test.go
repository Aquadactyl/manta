package filesystem

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zip"
	"github.com/mholt/archives"
	"github.com/pterodactyl/wings/internal/ufs"
	"github.com/stretchr/testify/require"
)

type spaceCheckFile struct {
	ufs.File
	info    ufs.FileInfo
	statErr error
	seekErr error
	closed  bool
}

func (f *spaceCheckFile) Stat() (ufs.FileInfo, error)    { return f.info, f.statErr }
func (f *spaceCheckFile) Seek(int64, int) (int64, error) { return 0, f.seekErr }
func (f *spaceCheckFile) Close() error                   { f.closed = true; return nil }
func (f *spaceCheckFile) Read([]byte) (int, error)       { return 0, io.EOF }

type spaceCheckArchive struct{ entries []archives.FileInfo }

func (spaceCheckArchive) Extension() string { return ".spacecheck" }
func (spaceCheckArchive) MediaType() string { return "application/x-spacecheck" }
func (spaceCheckArchive) Match(context.Context, string, io.Reader) (archives.MatchResult, error) {
	return archives.MatchResult{}, nil
}
func (a spaceCheckArchive) Extract(ctx context.Context, _ io.Reader, handle archives.FileHandler) error {
	for _, entry := range a.entries {
		if err := handle(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

type archiveSizeInfo struct {
	os.FileInfo
	size int64
}

func (i archiveSizeInfo) Size() int64 { return i.size }

func TestSpaceAvailableForDecompressionFileAndIdentifyErrors(t *testing.T) {
	fs, err := New(t.TempDir(), 1024, nil)
	require.NoError(t, err)
	defer fs.UnixFS().Close()

	oldOpen, oldIdentify, oldZipReader := filesystemOpen, identifyForSpaceCheck, newZipReaderForSpaceCheck
	t.Cleanup(func() {
		filesystemOpen, identifyForSpaceCheck, newZipReaderForSpaceCheck = oldOpen, oldIdentify, oldZipReader
	})

	t.Run("open error", func(t *testing.T) {
		want := errors.New("open failed")
		filesystemOpen = func(*ufs.Quota, string) (ufs.File, error) { return nil, want }
		require.ErrorIs(t, fs.SpaceAvailableForDecompression(context.Background(), "/", "archive.bin"), want)
	})

	t.Run("identify error", func(t *testing.T) {
		want := errors.New("identify failed")
		f := &spaceCheckFile{}
		filesystemOpen = func(*ufs.Quota, string) (ufs.File, error) { return f, nil }
		identifyForSpaceCheck = func(context.Context, string, io.Reader) (archives.Format, io.Reader, error) {
			return nil, nil, want
		}
		require.ErrorIs(t, fs.SpaceAvailableForDecompression(context.Background(), "/", "archive.bin"), want)
		require.True(t, f.closed)
	})

	t.Run("seek error", func(t *testing.T) {
		want := errors.New("seek failed")
		f := &spaceCheckFile{seekErr: want}
		filesystemOpen = func(*ufs.Quota, string) (ufs.File, error) { return f, nil }
		identifyForSpaceCheck = func(context.Context, string, io.Reader) (archives.Format, io.Reader, error) {
			return nil, nil, archives.NoMatch
		}
		require.ErrorIs(t, fs.SpaceAvailableForDecompression(context.Background(), "/", "archive.bin"), want)
		require.True(t, f.closed)
	})

	t.Run("stat error", func(t *testing.T) {
		want := errors.New("stat failed")
		f := &spaceCheckFile{statErr: want}
		filesystemOpen = func(*ufs.Quota, string) (ufs.File, error) { return f, nil }
		identifyForSpaceCheck = func(context.Context, string, io.Reader) (archives.Format, io.Reader, error) {
			return nil, nil, archives.NoMatch
		}
		require.ErrorIs(t, fs.SpaceAvailableForDecompression(context.Background(), "/", "archive.bin"), want)
		require.True(t, f.closed)
	})

	t.Run("canceled while adding an extracted entry", func(t *testing.T) {
		filesystemOpen = oldOpen
		archivePath := filepath.Join(fs.Path(), "canceled.spacecheck")
		require.NoError(t, os.WriteFile(archivePath, []byte("archive"), 0o600))
		info, err := os.Stat(archivePath)
		require.NoError(t, err)
		identifyForSpaceCheck = func(context.Context, string, io.Reader) (archives.Format, io.Reader, error) {
			return spaceCheckArchive{entries: []archives.FileInfo{{FileInfo: info, NameInArchive: "entry"}}}, nil, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, fs.SpaceAvailableForDecompression(ctx, "/", "canceled.spacecheck"), context.Canceled)
	})

	t.Run("decompressed size overflow is rejected", func(t *testing.T) {
		maxInt64 := int64(^uint64(0) >> 1)
		largeFS, err := New(t.TempDir(), maxInt64, nil)
		require.NoError(t, err)
		defer largeFS.UnixFS().Close()
		archivePath := filepath.Join(largeFS.Path(), "overflow.spacecheck")
		require.NoError(t, os.WriteFile(archivePath, []byte("archive"), 0o600))
		info, err := os.Stat(archivePath)
		require.NoError(t, err)
		filesystemOpen = func(q *ufs.Quota, name string) (ufs.File, error) { return oldOpen(q, name) }
		identifyForSpaceCheck = func(context.Context, string, io.Reader) (archives.Format, io.Reader, error) {
			return spaceCheckArchive{entries: []archives.FileInfo{
				{FileInfo: archiveSizeInfo{FileInfo: info, size: maxInt64}, NameInArchive: "large"},
				{FileInfo: archiveSizeInfo{FileInfo: info, size: 1}, NameInArchive: "overflow"},
			}}, nil, nil
		}
		err = largeFS.SpaceAvailableForDecompression(context.Background(), "/", "overflow.spacecheck")
		require.Error(t, err)
	})

	t.Run("ZIP64 entry larger than int64 is rejected", func(t *testing.T) {
		maxInt64 := uint64(^uint64(0) >> 1)
		path := filepath.Join(t.TempDir(), "archive")
		require.NoError(t, os.WriteFile(path, []byte("archive"), 0o600))
		info, err := os.Stat(path)
		require.NoError(t, err)
		f := &spaceCheckFile{info: info}
		filesystemOpen = func(*ufs.Quota, string) (ufs.File, error) { return f, nil }
		identifyForSpaceCheck = func(context.Context, string, io.Reader) (archives.Format, io.Reader, error) {
			return archives.Zip{}, nil, nil
		}
		newZipReaderForSpaceCheck = func(io.ReaderAt, int64) (*zip.Reader, error) {
			return &zip.Reader{File: []*zip.File{{FileHeader: zip.FileHeader{Name: "huge", UncompressedSize64: maxInt64 + 1}}}}, nil
		}
		err = fs.SpaceAvailableForDecompression(context.Background(), "/", "huge.zip")
		require.Error(t, err)
		require.True(t, f.closed)
	})
}
