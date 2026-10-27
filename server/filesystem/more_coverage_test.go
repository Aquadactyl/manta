package filesystem

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

func TestFilesystemErrors(t *testing.T) {
	// Test Error.Code()
	err1 := &Error{code: ErrCodeIsDirectory, resolved: "/dir"}
	assert.Equal(t, ErrCodeIsDirectory, err1.Code())
	assert.Contains(t, err1.Error(), "is a directory")

	errDisk := &Error{code: ErrCodeDiskSpace}
	assert.Contains(t, errDisk.Error(), "not enough disk space")

	errArch := &Error{code: ErrCodeUnknownArchive}
	assert.Contains(t, errArch.Error(), "unknown archive format")

	errDeny := &Error{code: ErrCodeDenylistFile, resolved: "forbidden.txt"}
	assert.Contains(t, errDeny.Error(), "is on the denylist")
	errDenyEmpty := &Error{code: ErrCodeDenylistFile}
	assert.Contains(t, errDenyEmpty.Error(), "<empty>")

	errNotExist := &Error{code: ErrNotExist}
	assert.Equal(t, "filesystem: does not exist", errNotExist.Error())

	errDefault := &Error{code: ErrCodeUnknownError, err: io.ErrUnexpectedEOF}
	assert.Contains(t, errDefault.Error(), "unexpected EOF")

	// IsFilesystemError
	wrapped := newFilesystemError(ErrCodeDiskSpace, nil)
	assert.True(t, IsFilesystemError(wrapped))
	assert.False(t, IsFilesystemError(io.EOF))
}

func TestFilesystemDiskSpaceChecks(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024, []string{})
	require.NoError(t, err)

	// Unlimited
	fs.SetDiskLimit(0)
	assert.True(t, fs.HasSpaceAvailable(false))
	assert.NoError(t, fs.HasSpaceErr(false))

	// Limited with room
	fs.SetDiskLimit(1000000000)
	assert.True(t, fs.HasSpaceAvailable(false))
	assert.NoError(t, fs.HasSpaceErr(false))

	// Limited and exceeded (set limit to 1 byte, but directory size is larger)
	// Create a 100 byte file
	err = fs.Writefile("large.txt", bytes.NewReader(bytes.Repeat([]byte("a"), 100)))
	require.NoError(t, err)

	fs.SetDiskLimit(10)
	assert.False(t, fs.HasSpaceAvailable(false))
	assert.Error(t, fs.HasSpaceErr(false))
}

func TestFilesystemDirectoryOperations(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024, []string{})
	require.NoError(t, err)

	require.NoError(t, fs.CreateDirectory("subdir", ""))
	require.NoError(t, fs.Writefile("subdir/file1.txt", bytes.NewReader([]byte("content1"))))
	require.NoError(t, fs.Writefile("subdir/file2.txt", bytes.NewReader([]byte("content2"))))

	// ReadDir
	entries, err := fs.ReadDir("subdir")
	require.NoError(t, err)
	assert.Len(t, entries, 2)

	// ReadDirStat
	stats, err := fs.ReadDirStat("subdir")
	require.NoError(t, err)
	assert.Len(t, stats, 2)

	// UnixFS
	assert.NotNil(t, fs.UnixFS())

	// Stat & MarshalJSON
	st, err := fs.Stat("subdir/file1.txt")
	require.NoError(t, err)
	assert.Equal(t, "file1.txt", st.Name())
	assert.Equal(t, int64(8), st.Size())

	jsonData, err := json.Marshal(&st)
	require.NoError(t, err)
	assert.Contains(t, string(jsonData), `"name":"file1.txt"`)
	assert.Contains(t, string(jsonData), `"size":8`)

	// Chmod
	err = fs.Chmod("subdir/file1.txt", 0600)
	require.NoError(t, err)

	// Chown
	err = fs.Chown("subdir/file1.txt")
	require.NoError(t, err)
}

func TestQuotaFileMethods(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024, []string{})
	require.NoError(t, err)

	// Touch returns a quotaFile wrapper
	w, err := fs.Touch("quota_test.txt", os.O_CREATE|os.O_RDWR)
	require.NoError(t, err)
	qf, ok := w.(*quotaFile)
	require.True(t, ok)

	// CheckSize
	assert.NoError(t, qf.CheckSize(500))

	// ReadFrom
	n, err := qf.ReadFrom(bytes.NewReader([]byte("hello quota world")))
	require.NoError(t, err)
	assert.Equal(t, int64(17), n)

	require.NoError(t, qf.Close())
}

func TestFilesystemMoreOps(t *testing.T) {
	cfg := &config.Configuration{
		AuthenticationToken: "tok",
		System: config.SystemConfiguration{
			DiskCheckInterval: 60,
		},
	}
	config.Set(cfg)

	// Error helpers
	assert.False(t, IsErrorCode(nil, ErrCodeUnknownError))
	assert.False(t, IsErrorCode(errors.New("other"), ErrCodeUnknownError))
	assert.True(t, IsErrorCode(NewBadPathResolution("p", "r"), ErrCodePathResolution))

	assert.Nil(t, wrapError(nil, ""))
	badPath := NewBadPathResolution("p", "r")
	assert.Equal(t, badPath, wrapError(badPath, ""))
	wrapped := wrapError(errors.New("other"), "res")
	assert.NotNil(t, wrapped)
	assert.True(t, IsErrorCode(wrapped, ErrCodeUnknownError))

	dir := t.TempDir()
	fs, err := New(dir, 1024*1024, []string{})
	require.NoError(t, err)

	// Create a file
	require.NoError(t, fs.Writefile("orig.txt", bytes.NewReader([]byte("sample content for copy"))))

	// Chtimes
	now := time.Now().Add(-time.Hour)
	require.NoError(t, fs.Chtimes("orig.txt", now, now))

	// Copy
	require.NoError(t, fs.Copy("orig.txt"))
	assert.FileExists(t, filepath.Join(dir, "orig copy.txt"))

	// Copy again to exercise suffix loop
	require.NoError(t, fs.Copy("orig.txt"))
	assert.FileExists(t, filepath.Join(dir, "orig copy 1.txt"))

	// DirectorySize
	sz, err := fs.DirectorySize("/")
	assert.NoError(t, err)
	assert.True(t, sz > 0)

	// DiskUsage
	usage, err := fs.DiskUsage(false)
	assert.NoError(t, err)
	assert.True(t, usage > 0)

	// Stat and CTime
	st, err := fs.Stat("orig.txt")
	require.NoError(t, err)
	assert.NotZero(t, st.CTime())
}

func TestCompressAndDecompressAdvanced(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test"})
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024*1024, []string{"denied.txt"})
	require.NoError(t, err)

	require.NoError(t, fs.Writefile("orig.txt", bytes.NewReader([]byte("sample data"))))

	// 1. CompressFiles multiple times creates archive.tar.gz and archive-1.tar.gz
	f1, err := fs.CompressFiles("/", []string{"orig.txt"})
	require.NoError(t, err)
	assert.Contains(t, f1.Name(), "archive-")

	f2, err := fs.CompressFiles("/", []string{"orig.txt"})
	require.NoError(t, err)
	assert.Contains(t, f2.Name(), "-1.tar.gz")

	// 2. DecompressFile with directory, symlink, and ignored file
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Directory entry
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "subfolder/", Mode: 0755, Typeflag: tar.TypeDir}))
	// Regular file
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "subfolder/hello.txt", Mode: 0644, Size: 5, Typeflag: tar.TypeReg}))
	_, err = tw.Write([]byte("world"))
	require.NoError(t, err)
	// Symlink
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "subfolder/sym.txt", Linkname: "hello.txt", Mode: 0777, Typeflag: tar.TypeSymlink}))
	// Symlink with empty target (skipped)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "subfolder/emptylink", Linkname: "", Mode: 0777, Typeflag: tar.TypeSymlink}))
	// Denylisted file (skipped)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "denied.txt", Mode: 0644, Size: 4, Typeflag: tar.TypeReg}))
	_, err = tw.Write([]byte("skip"))
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	require.NoError(t, fs.Writefile("complex.tar.gz", bytes.NewReader(buf.Bytes())))

	// SpaceAvailableForDecompression
	require.NoError(t, fs.SpaceAvailableForDecompression(context.Background(), "/", "complex.tar.gz"))

	// DecompressFile
	require.NoError(t, fs.DecompressFile(context.Background(), "/", "complex.tar.gz"))
	assert.FileExists(t, filepath.Join(dir, "subfolder", "hello.txt"))
	assert.FileExists(t, filepath.Join(dir, "subfolder", "sym.txt"))
	assert.NoFileExists(t, filepath.Join(dir, "denied.txt"))

	// 3. SpaceAvailableForDecompression with long entry name (> 4096 chars)
	var longBuf bytes.Buffer
	gwLong := gzip.NewWriter(&longBuf)
	twLong := tar.NewWriter(gwLong)
	longName := strings.Repeat("a", 4100)
	require.NoError(t, twLong.WriteHeader(&tar.Header{Name: longName, Mode: 0644, Size: 0, Typeflag: tar.TypeReg}))
	require.NoError(t, twLong.Close())
	require.NoError(t, gwLong.Close())

	require.NoError(t, fs.Writefile("longname.tar.gz", bytes.NewReader(longBuf.Bytes())))
	err = fs.SpaceAvailableForDecompression(context.Background(), "/", "longname.tar.gz")
	assert.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "too long")
	}
}

