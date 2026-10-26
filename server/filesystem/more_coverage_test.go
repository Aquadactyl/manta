package filesystem

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
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
