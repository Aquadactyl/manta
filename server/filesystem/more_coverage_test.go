package filesystem

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mholt/archives"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/progress"
	"github.com/pterodactyl/wings/internal/ufs"
)

func init() {
	config.Set(&config.Configuration{
		AuthenticationToken: "abc",
		System: config.SystemConfiguration{
			RootDirectory:     "/tmp",
			DiskCheckInterval: 150,
			Backups: config.Backups{
				WriteLimit:       0,
				CompressionLevel: "gzip",
			},
		},
	})
}

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

func TestTarProgress(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "test.txt", Size: 8, Mode: 0644}))
	tpNil := NewTarProgress(tw, nil)
	assert.Nil(t, tpNil.p)
	n, err := tpNil.Write([]byte("data"))
	require.NoError(t, err)
	assert.Equal(t, 4, n)

	prog := progress.NewProgress(100)
	tp := NewTarProgress(tw, prog)
	assert.NotNil(t, tp.p)
	n, err = tp.Write([]byte("more"))
	require.NoError(t, err)
	assert.Equal(t, 4, n)
}

func TestArchiveEdgeCases(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024*1024, []string{})
	require.NoError(t, err)

	nilArch := &Archive{}
	assert.Error(t, nilArch.Stream(context.Background(), io.Discard))

	cfg := config.Get()
	oldWriteLimit := cfg.System.Backups.WriteLimit
	oldLevel := cfg.System.Backups.CompressionLevel
	cfg.System.Backups.WriteLimit = 10
	cfg.System.Backups.CompressionLevel = "none"
	config.Set(cfg)

	require.NoError(t, fs.Writefile("test.txt", strings.NewReader("content")))
	dstArchive := filepath.Join(t.TempDir(), "archive.tar.gz")
	arch := &Archive{
		Filesystem: fs,
		Files: []string{
			filepath.Join(fs.Path(), "test.txt"),
			"test.txt",
		},
	}
	assert.NoError(t, arch.Create(context.Background(), dstArchive))

	cfg.System.Backups.CompressionLevel = "best_compression"
	config.Set(cfg)
	dstArchive2 := filepath.Join(t.TempDir(), "archive2.tar.gz")
	assert.NoError(t, arch.Create(context.Background(), dstArchive2))

	cfg.System.Backups.WriteLimit = oldWriteLimit
	cfg.System.Backups.CompressionLevel = oldLevel
	config.Set(cfg)

	largeFile := filepath.Join(dir, "large.dat")
	require.NoError(t, os.WriteFile(largeFile, make([]byte, 256*1024), 0644))
	archLarge := &Archive{
		Filesystem: fs,
		Files:      []string{"large.dat"},
	}
	dstLarge := filepath.Join(t.TempDir(), "large.tar.gz")
	assert.NoError(t, archLarge.Create(context.Background(), dstLarge))

	ctxCancel, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, archLarge.Stream(ctxCancel, io.Discard))
}

func TestDecompressZipAndSingleFile(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024*1024, []string{"denied.txt"})
	require.NoError(t, err)

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	f1, err := zw.Create("folder/file1.txt")
	require.NoError(t, err)
	_, err = f1.Write([]byte("zip file 1"))
	require.NoError(t, err)
	f2, err := zw.Create("denied.txt")
	require.NoError(t, err)
	_, err = f2.Write([]byte("denied content"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	require.NoError(t, fs.Writefile("archive.zip", bytes.NewReader(zipBuf.Bytes())))

	assert.NoError(t, fs.SpaceAvailableForDecompression(context.Background(), "/", "archive.zip"))
	assert.NoError(t, fs.DecompressFile(context.Background(), "/", "archive.zip"))
	assert.FileExists(t, filepath.Join(dir, "folder", "file1.txt"))
	assert.NoFileExists(t, filepath.Join(dir, "denied.txt"))

	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	_, err = gw.Write([]byte("single compressed file text"))
	require.NoError(t, err)
	require.NoError(t, gw.Close())

	require.NoError(t, fs.Writefile("single.txt.gz", bytes.NewReader(gzBuf.Bytes())))
	assert.NoError(t, fs.SpaceAvailableForDecompression(context.Background(), "/", "single.txt.gz"))
	assert.NoError(t, fs.DecompressFile(context.Background(), "/", "single.txt.gz"))
	assert.FileExists(t, filepath.Join(dir, "single.txt"))

	var tarGzBuf bytes.Buffer
	gwTar := gzip.NewWriter(&tarGzBuf)
	twTar := tar.NewWriter(gwTar)
	require.NoError(t, twTar.WriteHeader(&tar.Header{Name: "extracted.txt", Size: 4, Mode: 0644}))
	_, err = twTar.Write([]byte("extr"))
	require.NoError(t, err)
	require.NoError(t, twTar.Close())
	require.NoError(t, gwTar.Close())

	assert.NoError(t, fs.ExtractTransfer(context.Background(), bytes.NewReader(tarGzBuf.Bytes())))
	assert.Error(t, fs.ExtractTransfer(context.Background(), strings.NewReader("not an archive")))

	fs.SetDiskLimit(0)
	assert.NoError(t, fs.SpaceAvailableForDecompression(context.Background(), "/", "archive.zip"))
	fs.SetDiskLimit(1024 * 1024 * 1024)

	require.NoError(t, fs.Writefile("unknown.bin", strings.NewReader("not an archive")))
	assert.Error(t, fs.SpaceAvailableForDecompression(context.Background(), "/", "unknown.bin"))
	assert.Error(t, fs.DecompressFile(context.Background(), "/", "unknown.bin"))

	fs.SetDiskLimit(10)
	assert.Error(t, fs.SpaceAvailableForDecompression(context.Background(), "/", "archive.zip"))
	fs.SetDiskLimit(1024 * 1024 * 1024)
}

func TestDiskSpaceBackgroundLookup(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024, []string{})
	require.NoError(t, err)

	fs.diskCheckInterval = 0
	usage, err := fs.DiskUsage(false)
	require.NoError(t, err)
	assert.Equal(t, int64(0), usage)

	fs.diskCheckInterval = 1 * time.Second
	fs.lastLookupTime.Set(time.Now().Add(-10 * time.Minute))
	usage, err = fs.DiskUsage(true)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, usage, int64(0))

	fs.SetDiskLimit(100)
	assert.Error(t, fs.reserveDisk(200))
	assert.NoError(t, fs.reserveDisk(50))
	fs.releaseDisk(50, 0)
	fs.releaseDisk(0, 0)

	// DirectorySize
	require.NoError(t, fs.Writefile("f1.txt", strings.NewReader("12345")))
	size, err := fs.DirectorySize("/")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, size, int64(5))

	_, err = fs.DirectorySize("../../outside")
	assert.Error(t, err)
}

type dummyFileInfo struct {
	os.FileInfo
}

func (d *dummyFileInfo) Sys() any {
	return "not-stat-t"
}

type syscallFileInfo struct {
	os.FileInfo
}

func (s *syscallFileInfo) Sys() any {
	return &syscall.Stat_t{Ctim: syscall.Timespec{Sec: 123456789, Nsec: 0}}
}

func TestFilesystemWriteAndCopyEdgeCases(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024, []string{"denied.txt"})
	require.NoError(t, err)

	require.NoError(t, os.Mkdir(filepath.Join(dir, "my_dir"), 0755))
	assert.Error(t, fs.Writefile("my_dir", strings.NewReader("data")))
	assert.Error(t, fs.Write("my_dir", strings.NewReader("data"), 4, 0644))
	_, err = fs.WriteFrom("my_dir", strings.NewReader("data"))
	assert.Error(t, err)

	fs.SetDiskLimit(10)
	assert.Error(t, fs.Write("big.txt", strings.NewReader("this text is longer than 10 bytes"), 50, 0644))
	fs.SetDiskLimit(1024 * 1024)

	require.NoError(t, fs.Writefile("file.txt", strings.NewReader("content")))
	dirfd, _, closeFd, err := fs.unixFS.SafePath("file.txt")
	require.NoError(t, err)
	defer closeFd()

	name1, err := fs.findCopySuffix(dirfd, "file", ".txt")
	require.NoError(t, err)
	assert.Equal(t, "file copy.txt", name1)

	require.NoError(t, fs.Writefile("file copy.txt", strings.NewReader("content")))
	name2, err := fs.findCopySuffix(dirfd, "file", ".txt")
	require.NoError(t, err)
	assert.Equal(t, "file copy 1.txt", name2)

	assert.Error(t, fs.Copy("nonexistent.txt"))
	assert.Error(t, fs.Copy("my_dir"))

	require.NoError(t, fs.Writefile("src.txt", strings.NewReader("source content")))
	assert.NoError(t, fs.Copy("src.txt"))
	assert.FileExists(t, filepath.Join(dir, "src copy.txt"))
	assert.NoError(t, fs.Copy("src.txt"))
	assert.FileExists(t, filepath.Join(dir, "src copy 1.txt"))

	fs.isTest = true
	assert.NoError(t, fs.Chown("/"))
	if os.Geteuid() == 0 {
		fs.isTest = false
		assert.NoError(t, fs.Chown("/"))
		fs.isTest = true
	}

	entries, err := fs.ListDirectory("/")
	require.NoError(t, err)
	assert.NotEmpty(t, entries)

	_, err = fs.ListDirectory("/nonexistent_dir")
	assert.Error(t, err)

	_, err = fs.Stat("does_not_exist.txt")
	assert.Error(t, err)

	st, err := fs.Stat("src.txt")
	require.NoError(t, err)
	assert.Equal(t, "src.txt", st.Name())
	assert.False(t, st.IsDir())
	assert.False(t, st.CTime().IsZero())

	// Stat CTime with dummy and syscall sys
	nonStat := &Stat{FileInfo: &dummyFileInfo{}}
	assert.True(t, nonStat.CTime().IsZero())

	sysStat := &Stat{FileInfo: &syscallFileInfo{}}
	assert.False(t, sysStat.CTime().IsZero())

	// TruncateRootDirectory error with immutable file
	immFile := filepath.Join(dir, "imm_truncate.txt")
	require.NoError(t, os.WriteFile(immFile, []byte("imm"), 0644))
	if exec.Command("chattr", "+i", immFile).Run() == nil {
		assert.Error(t, fs.TruncateRootDirectory())
		_ = exec.Command("chattr", "-i", immFile).Run()
	}
}

func TestQuotaFileMoreWriteMethods(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 100, []string{})
	require.NoError(t, err)

	f, err := fs.unixFS.OpenFile("quota_test.txt", ufs.O_RDWR|ufs.O_CREATE, 0644)
	require.NoError(t, err)

	qf := newQuotaFile(fs, f, 100)
	n, err := qf.Write([]byte("hello world"))
	require.NoError(t, err)
	assert.Equal(t, 11, n)

	n, err = qf.WriteAt([]byte("HELLO"), 0)
	require.NoError(t, err)
	assert.Equal(t, 5, n)

	_, err = qf.Write(make([]byte, 200))
	assert.Error(t, err)

	n0, err := qf.Write([]byte{})
	assert.NoError(t, err)
	assert.Equal(t, 0, n0)

	n0, err = qf.WriteAt([]byte{}, 0)
	assert.NoError(t, err)
	assert.Equal(t, 0, n0)

	_, err = qf.WriteAt([]byte("a"), -1)
	assert.Error(t, err)

	assert.NoError(t, qf.(*quotaFile).CheckSize(50))
	assert.NoError(t, qf.(*quotaFile).CheckSize(100))

	require.NoError(t, qf.Close())
}

func TestArchiveSocketAndErrors(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024*1024, []string{})
	require.NoError(t, err)

	require.NoError(t, fs.Writefile("f.txt", strings.NewReader("hello")))

	arch := &Archive{Filesystem: fs, Files: []string{"f.txt"}}
	err = arch.Create(context.Background(), "/nonexistent/path/dir/archive.tar.gz")
	assert.Error(t, err)

	archInvalid := &Archive{Filesystem: fs, BaseDirectory: "../../outside"}
	assert.Error(t, archInvalid.Stream(context.Background(), io.Discard))

	l, err := net.Listen("unix", filepath.Join(dir, "my.sock"))
	if err == nil {
		defer l.Close()
		dst := filepath.Join(t.TempDir(), "sock.tar.gz")
		archSock := &Archive{Filesystem: fs}
		assert.NoError(t, archSock.Create(context.Background(), dst))
	}
}

func TestCompressAndDecompressAdvancedEdgeCases(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024*1024, []string{"denied.txt"})
	require.NoError(t, err)

	require.NoError(t, fs.Writefile("f.txt", strings.NewReader("content")))

	_, err = fs.CompressFiles("/invalid\x00path", []string{"f.txt"})
	assert.Error(t, err)

	var bufTar bytes.Buffer
	tw := tar.NewWriter(&bufTar)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "f.txt", Size: 7, Mode: 0644}))
	_, _ = tw.Write([]byte("content"))
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "hardlink.txt", Typeflag: tar.TypeLink, Linkname: "f.txt"}))
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "emptysymlink.txt", Typeflag: tar.TypeSymlink, Linkname: ""}))
	require.NoError(t, tw.Close())

	require.NoError(t, fs.Writefile("test.tar", bytes.NewReader(bufTar.Bytes())))
	assert.NoError(t, fs.DecompressFile(context.Background(), "/", "test.tar"))

	ctxCancel, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, fs.SpaceAvailableForDecompression(ctxCancel, "/", "test.tar"))

	// Single gz denied
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	_, _ = gw.Write([]byte("denied content"))
	require.NoError(t, gw.Close())
	require.NoError(t, fs.Writefile("denied.txt.gz", bytes.NewReader(gzBuf.Bytes())))
	assert.NoError(t, fs.DecompressFile(context.Background(), "/", "denied.txt.gz"))
	assert.NoFileExists(t, filepath.Join(dir, "denied.txt"))

	// Corrupted gz
	corrupted := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0x00, 0x00}
	require.NoError(t, fs.Writefile("corrupted.gz", bytes.NewReader(corrupted)))
	assert.Error(t, fs.DecompressFile(context.Background(), "/", "corrupted.gz"))
}

func TestFilesystemChownChtimesAndFile(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024*1024, []string{})
	require.NoError(t, err)

	require.NoError(t, fs.Writefile("src.txt", strings.NewReader("sample")))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "aaa_dir"), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "zzz_dir"), 0755))

	cfg := config.Get()
	oldUid, oldGid := cfg.System.User.Uid, cfg.System.User.Gid
	cfg.System.User.Uid = os.Getuid()
	cfg.System.User.Gid = os.Getgid()
	config.Set(cfg)
	defer func() {
		cfg.System.User.Uid = oldUid
		cfg.System.User.Gid = oldGid
		config.Set(cfg)
	}()

	if os.Geteuid() == 0 {
		fs.isTest = false
		assert.NoError(t, fs.Chown("/"))
		assert.NoError(t, fs.Chown("src.txt"))
		assert.NoError(t, fs.Chtimes("src.txt", time.Now(), time.Now()))
		assert.NoError(t, fs.Copy("src.txt"))
		fs.isTest = true
	}

	f, st, err := fs.File("src.txt")
	require.NoError(t, err)
	_ = f.Close()
	assert.Equal(t, "src.txt", st.Name())
	assert.False(t, st.IsDir())

	fDir, stDir, err := fs.File("/")
	require.NoError(t, err)
	_ = fDir.Close()
	assert.True(t, stDir.IsDir())
	assert.Equal(t, "inode/directory", stDir.Mimetype)

	// Hard link DirectorySize
	require.NoError(t, os.Link(filepath.Join(dir, "src.txt"), filepath.Join(dir, "src_link.txt")))
	size, err := fs.DirectorySize("/")
	require.NoError(t, err)
	assert.Greater(t, size, int64(0))

	// Multi-extension .tar.gz in Copy
	require.NoError(t, fs.Writefile("test.tar.gz", strings.NewReader("archive-data")))
	assert.NoError(t, fs.Copy("test.tar.gz"))
	assert.FileExists(t, filepath.Join(dir, "test copy.tar.gz"))

	// ListDirectory sorting
	entries, err := fs.ListDirectory("/")
	require.NoError(t, err)
	assert.Greater(t, len(entries), 3)
}

type mockDirEntry struct {
	ufs.DirEntry
	infoErr error
	isDir   bool
}

func (m *mockDirEntry) IsDir() bool {
	return m.isDir
}

func (m *mockDirEntry) Info() (os.FileInfo, error) {
	return nil, m.infoErr
}

func TestArchiveAddToArchiveAndCallbackEdges(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024*1024, []string{})
	require.NoError(t, err)

	arch := &Archive{Filesystem: fs}
	// Option returns custom error (line 239)
	cb := arch.callback(func(int, string, string, ufs.DirEntry) error {
		return errors.New("custom error")
	})
	assert.NoError(t, cb(0, "dir", "dir", &mockDirEntry{isDir: true}))
	assert.Error(t, cb(0, "test", "test", &mockDirEntry{isDir: false}))

	// addToArchive with entry.Info() returning ErrNotExist
	assert.NoError(t, arch.addToArchive(0, "nonexistent", "nonexistent", &mockDirEntry{infoErr: ufs.ErrNotExist}))

	// addToArchive with entry.Info() returning generic error
	assert.Error(t, arch.addToArchive(0, "nonexistent", "nonexistent", &mockDirEntry{infoErr: errors.New("lstat error")}))
}

type dummyArchiveFormat struct{}

func (dummyArchiveFormat) Extension() string { return ".dummy" }
func (dummyArchiveFormat) MediaType() string { return "application/x-dummy" }
func (dummyArchiveFormat) Match(context.Context, string, io.Reader) (archives.MatchResult, error) {
	return archives.MatchResult{}, nil
}

func TestCompressAndDecompressCancelAndFormat(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024*1024, []string{})
	require.NoError(t, err)

	require.NoError(t, fs.Writefile("test.tar", strings.NewReader("dummy tar content")))

	ctxCancel, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, fs.DecompressFile(ctxCancel, "/", "test.tar"))

	// Format not Extractor and not Decompressor returns nil (line 231)
	err = fs.extractStream(context.Background(), extractStreamOptions{
		Format: dummyArchiveFormat{},
	})
	assert.NoError(t, err)

	// mkdirAll with existing file as intermediate path element (line 312)
	require.NoError(t, fs.Writefile("file_not_dir.txt", strings.NewReader("content")))
	assert.Error(t, fs.mkdirAll("file_not_dir.txt/nested", 0755))

	// Copy multiple copies
	require.NoError(t, fs.Writefile("item.txt", strings.NewReader("content")))
	assert.NoError(t, fs.Copy("item.txt"))
	assert.NoError(t, fs.Copy("item.txt"))

	// ListDirectory sorting with file before dir alphabetically (line 585)
	dirSort := t.TempDir()
	fsSort, err := New(dirSort, 1024*1024*1024, []string{})
	require.NoError(t, err)
	require.NoError(t, fsSort.Writefile("00_file.txt", strings.NewReader("file")))
	require.NoError(t, os.Mkdir(filepath.Join(dirSort, "zz_dir"), 0755))
	entries, err := fsSort.ListDirectory("/")
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.True(t, entries[0].IsDir())
	assert.False(t, entries[1].IsDir())
}

func TestDiskSpaceBackgroundLookupGoroutine(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024*1024, []string{})
	require.NoError(t, err)

	fs.diskCheckInterval = 1
	fs.lastLookupTime.Set(time.Now().Add(-10 * time.Minute))
	usage, err := fs.DiskUsage(true)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, usage, int64(0))
	time.Sleep(50 * time.Millisecond)
}

func TestMoreCompressAndQuotaFileEdges(t *testing.T) {
	dir := t.TempDir()
	fs, err := New(dir, 1024*1024, []string{})
	require.NoError(t, err)

	// compress with disk limit exceeded in Stream (compress.go:60-64)
	require.NoError(t, fs.Writefile("large.txt", strings.NewReader(strings.Repeat("a", 1000))))
	fs.SetDiskLimit(10)
	_, err = fs.CompressFiles("/", []string{"large.txt"})
	assert.Error(t, err)
	fs.SetDiskLimit(1024 * 1024)

	// corrupted zip in SpaceAvailableForDecompression (compress.go:138-140)
	require.NoError(t, fs.Writefile("corrupted.zip", strings.NewReader("PK\x03\x04corrupted-zip-data")))
	assert.Error(t, fs.SpaceAvailableForDecompression(context.Background(), "/", "corrupted.zip"))

	// qf.Write on closed file fails Seek (quota_file.go:32-34)
	f, err := fs.unixFS.OpenFile("seek_err.txt", ufs.O_RDWR|ufs.O_CREATE, 0644)
	require.NoError(t, err)
	qf := newQuotaFile(fs, f, 0)
	_ = f.Close()
	_, err = qf.Write([]byte("test"))
	assert.Error(t, err)

	// SetTest
	fs.SetTest(true)
	assert.True(t, fs.isTest)
	fs.SetTest(false)
	assert.False(t, fs.isTest)

	// Stat on directory
	require.NoError(t, fs.unixFS.Mkdir("testdir", 0755))
	st, err := fs.Stat("testdir")
	require.NoError(t, err)
	assert.Equal(t, "inode/directory", st.Mimetype)

	// HasSpaceAvailable with MaxDisk == 0
	fs.SetDiskLimit(0)
	assert.True(t, fs.HasSpaceAvailable(true))

	// releaseDisk with reserved < 0 and with walking == true
	fs.releaseDisk(-50, 0)
	fs.walking = true
	fs.walkReserved = 100
	fs.releaseDisk(50, 50)
	assert.Equal(t, int64(50), fs.walkReserved)
	fs.walking = false

	// SpaceAvailableForDecompression with unknown archive
	require.NoError(t, fs.Writefile("plain.txt", strings.NewReader("plain text not archive")))
	fs.SetDiskLimit(1024 * 1024)
	err = fs.SpaceAvailableForDecompression(context.Background(), "/", "plain.txt")
	assert.True(t, IsErrorCode(err, ErrCodeUnknownArchive))

	// DecompressFile single gzip file
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	_, _ = gw.Write([]byte("single gzipped content"))
	_ = gw.Close()
	require.NoError(t, fs.Writefile("single.txt.gz", bytes.NewReader(gzBuf.Bytes())))
	assert.NoError(t, fs.DecompressFile(context.Background(), "/", "single.txt.gz"))

	// ExtractTransfer with unknown archive format
	err = fs.ExtractTransfer(context.Background(), strings.NewReader("bad bytes"))
	assert.Error(t, err)

	// New with zero disk limit
	fsZero, err := New(t.TempDir(), 0, []string{})
	assert.NoError(t, err)
	assert.Equal(t, int64(0), fsZero.MaxDisk())
}
