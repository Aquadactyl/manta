package ufs

import (
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

type fakeDirEntry struct {
	name  string
	isDir bool
}

func (f fakeDirEntry) Name() string            { return f.name }
func (f fakeDirEntry) IsDir() bool             { return f.isDir }
func (f fakeDirEntry) Type() FileMode          { return ModeDir }
func (f fakeDirEntry) Info() (FileInfo, error) { return nil, nil }

type errReadDirFS struct {
	Filesystem
	readDirErr error
}

func (e errReadDirFS) ReadDir(name string) ([]DirEntry, error) {
	return nil, e.readDirErr
}

type eofWriteFile struct {
	File
}

func (e eofWriteFile) Write(p []byte) (int, error) {
	return len(p), io.EOF
}

func TestUFSRemainingCoverage(t *testing.T) {
	// 1. syscallMode
	m := syscallMode(ModeSetuid | ModeSetgid | ModeSticky | 0o755)
	assert.NotZero(t, m&unix.S_ISUID)
	assert.NotZero(t, m&unix.S_ISGID)
	assert.NotZero(t, m&unix.S_ISVTX)

	// 2. fillFileStatFromSys
	modes := []uint32{
		unix.S_IFBLK | unix.S_ISGID | unix.S_ISUID | unix.S_ISVTX,
		unix.S_IFCHR,
		unix.S_IFDIR,
		unix.S_IFIFO,
		unix.S_IFLNK,
		unix.S_IFREG,
		unix.S_IFSOCK,
	}
	for _, rawMode := range modes {
		var st fileStat
		st.sys.Mode = toSysMode(rawMode)
		fillFileStatFromSys(&st, "test")
		assert.Equal(t, "test", st.Name())
	}

	// 3. Quota edge cases
	base := t.TempDir()
	ufs, err := NewUnixFS(base, false)
	require.NoError(t, err)
	q := NewQuota(ufs, 100)
	t.Cleanup(func() { _ = q.Close() })

	assert.Equal(t, int64(0), q.Add(0))
	q.SetUsage(math.MaxInt64 - 5)
	assert.Equal(t, int64(math.MaxInt64), q.Add(10))
	assert.Equal(t, int64(0), q.Add(math.MinInt64))
	q.SetUsage(5)
	assert.Equal(t, int64(0), q.Add(-10))
	q.SetUsage(50)
	assert.Equal(t, int64(30), q.Add(-20))

	// CanFit branches
	q.SetLimit(-1)
	assert.False(t, q.CanFit(1))
	q.SetLimit(0)
	assert.True(t, q.CanFit(1000))
	q.SetLimit(100)
	q.SetUsage(-1)
	assert.True(t, q.CanFit(50))
	q.SetUsage(50)
	assert.True(t, q.CanFit(0))
	assert.True(t, q.CanFit(-5))
	assert.True(t, q.CanFit(50))
	assert.False(t, q.CanFit(51))
	q.SetUsage(100)
	assert.False(t, q.CanFit(1))

	// Quota RemoveAll & freedBy branches
	assert.Error(t, q.RemoveAll(".."))
	assert.ErrorIs(t, q.RemoveAll("."), ErrBadPathResolution)
	require.NoError(t, q.Mkdir("emptydir", 0o755))
	require.NoError(t, q.Remove("emptydir"))
	assert.Error(t, q.Remove("nonexistent"))

	f1, err := q.Create("hard1.txt")
	require.NoError(t, err)
	_, _ = f1.Write([]byte("hello"))
	_ = f1.Close()
	require.NoError(t, unix.Link(filepath.Join(base, "hard1.txt"), filepath.Join(base, "hard2.txt")))
	q.SetUsage(5)
	require.NoError(t, q.Remove("hard1.txt"))
	assert.Equal(t, int64(5), q.Usage()) // Nlink > 1 frees 0 bytes
	require.NoError(t, q.Remove("hard2.txt"))
	assert.Equal(t, int64(0), q.Usage())

	// 4. CountedWriter & CountedReader
	fCount, err := ufs.Create("counted.txt")
	require.NoError(t, err)
	cw := NewCountedWriter(eofWriteFile{File: fCount})
	n, err := cw.Write([]byte("abc"))
	assert.Equal(t, 3, n)
	assert.ErrorIs(t, err, io.EOF)
	assert.NoError(t, cw.Error())
	_, err = cw.Write([]byte("more"))
	assert.ErrorIs(t, err, io.EOF)
	cw.err = errors.New("write boom")
	assert.EqualError(t, cw.Error(), "write boom")
	_ = fCount.Close()

	cr := NewCountedReader(strings.NewReader("xyz"))
	buf := make([]byte, 10)
	_, _ = cr.Read(buf)
	_, err = cr.Read(buf)
	assert.ErrorIs(t, err, io.EOF)
	assert.NoError(t, cr.Error())
	_, err = cr.Read(buf)
	assert.ErrorIs(t, err, io.EOF)
	cr.err = errors.New("read boom")
	assert.EqualError(t, cr.Error(), "read boom")

	// 5. WalkDir (walk.go)
	assert.NoError(t, WalkDir(ufs, "missing_root", func(path string, d DirEntry, err error) error {
		return SkipDir
	}))
	_, err = ufs.MkdirAll("walkdir/sub", 0o755)
	require.NoError(t, err)
	fw, err := ufs.Create("walkdir/sub/f.txt")
	require.NoError(t, err)
	_ = fw.Close()

	assert.NoError(t, WalkDir(ufs, "walkdir", func(path string, d DirEntry, err error) error {
		if path == "walkdir/sub" {
			return SkipDir
		}
		return nil
	}))
	assert.NoError(t, WalkDir(ufs, "walkdir", func(path string, d DirEntry, err error) error {
		if path == "walkdir/sub/f.txt" {
			return SkipDir
		}
		return nil
	}))
	customErr := errors.New("stop walk")
	assert.ErrorIs(t, WalkDir(ufs, "walkdir", func(path string, d DirEntry, err error) error {
		if path == "walkdir/sub/f.txt" {
			return customErr
		}
		return nil
	}), customErr)

	errFS := errReadDirFS{Filesystem: ufs, readDirErr: errors.New("readdir fail")}
	assert.NoError(t, WalkDir(errFS, "walkdir", func(path string, d DirEntry, err error) error {
		if err != nil {
			return SkipDir
		}
		return nil
	}))
	assert.Error(t, WalkDir(errFS, "walkdir", func(path string, d DirEntry, err error) error {
		return err
	}))

	// 6. WalkDirat, ReadDirMap, dirent, modeTypeFromDirent (walk_unix.go)
	assert.NoError(t, ufs.WalkDirat(-1, "missing", func(dirfd int, name, relative string, d DirEntry, err error) error {
		return SkipDir
	}))
	dirfd, _, closeFd, err := ufs.SafePath(".")
	require.NoError(t, err)
	defer closeFd()

	assert.NoError(t, ufs.WalkDirat(dirfd, "walkdir", func(dirfd int, name, relative string, d DirEntry, err error) error {
		if relative == "sub" {
			return SkipDir
		}
		return nil
	}))
	assert.NoError(t, ufs.WalkDirat(dirfd, "walkdir", func(dirfd int, name, relative string, d DirEntry, err error) error {
		if name == "f.txt" {
			return SkipDir
		}
		return nil
	}))
	assert.ErrorIs(t, ufs.WalkDirat(dirfd, "walkdir", func(dirfd int, name, relative string, d DirEntry, err error) error {
		if name == "f.txt" {
			return customErr
		}
		return nil
	}), customErr)

	// walkDir when openat fails on directory
	assert.NoError(t, ufs.walkDir(nil, dirfd, "nonexistent_dir_entry", ".", fakeDirEntry{name: "nonexistent_dir_entry", isDir: true}, func(dirfd int, name, relative string, d DirEntry, err error) error {
		if err != nil {
			return SkipDir
		}
		return nil
	}))

	// ReadDirMap errors
	_, err = ReadDirMap(ufs, "..", func(d DirEntry) (string, error) { return d.Name(), nil })
	assert.Error(t, err)
	_, err = ReadDirMap(ufs, "missing_dir", func(d DirEntry) (string, error) { return d.Name(), nil })
	assert.Error(t, err)
	_, err = ReadDirMap(ufs, "walkdir", func(d DirEntry) (string, error) { return "", customErr })
	assert.ErrorIs(t, err, customErr)

	// nameFromDirent without null terminator
	var de unix.Dirent
	for i := range de.Name {
		de.Name[i] = 'a'
	}
	de.Reclen = uint16(nameOffset + len(de.Name))
	assert.Len(t, nameFromDirent(&de), len(de.Name))

	// modeTypeFromDirent all types
	for _, dt := range []uint8{unix.DT_REG, unix.DT_DIR, unix.DT_LNK, unix.DT_CHR, unix.DT_BLK, unix.DT_FIFO, unix.DT_SOCK} {
		_, err := ufs.modeTypeFromDirent(&unix.Dirent{Type: dt}, dirfd, "walkdir")
		assert.NoError(t, err)
	}
	_, err = ufs.modeTypeFromDirent(&unix.Dirent{Type: unix.DT_UNKNOWN}, dirfd, "walkdir")
	assert.NoError(t, err)
	_, err = ufs.modeTypeFromDirent(&unix.Dirent{Type: unix.DT_UNKNOWN}, dirfd, "missing_entry")
	assert.Error(t, err)

	// dirent with nil fs
	var nilDe dirent
	fi, err := nilDe.Info()
	assert.Nil(t, fi)
	assert.NoError(t, err)
	fNil, err := nilDe.Open()
	assert.Nil(t, fNil)
	assert.NoError(t, err)
}

func TestUFSUnixAndRemoveAllBranches(t *testing.T) {
	base := t.TempDir()
	ufs, err := NewUnixFS(base, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ufs.Close() })

	f, err := ufs.Create("reg.txt")
	require.NoError(t, err)
	_ = f.Close()

	dirfd, _, closeFd, err := ufs.SafePath(".")
	require.NoError(t, err)
	defer closeFd()

	// Chmod, fchmodNoFollow, Chown, Chtimes
	assert.Error(t, ufs.Chmod("..", 0o644))
	assert.NoError(t, fchmodNoFollow(dirfd, "reg.txt", 0o600))
	assert.Error(t, fchmodNoFollow(dirfd, "missing.txt", 0o600))
	require.NoError(t, ufs.Symlink("reg.txt", "sym.txt"))
	assert.ErrorIs(t, fchmodNoFollow(dirfd, "sym.txt", 0o600), unix.EOPNOTSUPP)

	assert.Error(t, ufs.Chown("..", -1, -1))
	assert.Error(t, ufs.Chtimes("..", time.Now(), time.Now()))
	assert.NoError(t, ufs.Chtimes("reg.txt", time.Time{}, time.Now()))

	// MkdirAll branches
	assert.Error(t, func() error { _, err := ufs.MkdirAll("..", 0o755); return err }())
	_, err = ufs.MkdirAll("reg.txt", 0o755)
	assert.ErrorIs(t, err, ErrNotDirectory)
	require.NoError(t, ufs.Symlink("..", "sym_out"))
	_, err = ufs.MkdirAll("sym_out", 0o755)
	assert.ErrorIs(t, err, ErrNotDirectory)
	require.NoError(t, ufs.Symlink("missing_target", "sym_miss"))
	_, err = ufs.MkdirAll("sym_miss", 0o755)
	assert.Error(t, err)
	created, err := ufs.MkdirAll("nested/dir/slash/", 0o755)
	assert.NoError(t, err)
	assert.NotEmpty(t, created)

	// OpenFile & OpenFileat with ".."
	fDot, err := ufs.OpenFile("nested/../reg.txt", O_RDONLY, 0)
	require.NoError(t, err)
	_ = fDot.Close()

	fAt, err := ufs.OpenFileat(dirfd, "nested/../reg.txt", O_RDONLY, 0)
	require.NoError(t, err)
	_ = fAt.Close()
	_, err = ufs.OpenFileat(-1, "missing", O_RDONLY, 0)
	assert.Error(t, err)

	// ReadDir errors
	_, err = ufs.ReadDir("..")
	assert.Error(t, err)
	_, err = ufs.ReadDir("missing_dir")
	assert.Error(t, err)

	// RemoveStat, Remove, RemoveAll, RemoveContents
	_, err = ufs.RemoveStat("..")
	assert.Error(t, err)
	_, err = ufs.RemoveStat("missing")
	assert.Error(t, err)
	_, err = ufs.RemoveStat("nested") // non-empty dir fails unlinkat(AT_REMOVEDIR)
	assert.Error(t, err)

	assert.Error(t, ufs.Remove(".."))
	assert.ErrorIs(t, ufs.Remove("."), ErrBadPathResolution)
	assert.Error(t, ufs.Remove("nested"))
	require.NoError(t, os.Mkdir(filepath.Join(base, "emptydir"), 0o755))
	assert.NoError(t, ufs.Remove("emptydir"))

	assert.Error(t, ufs.RemoveAll(".."))
	assert.ErrorIs(t, ufs.RemoveAll("."), ErrBadPathResolution)
	assert.Error(t, ufs.RemoveContents(".."))

	// removeAll & removeContents direct edge cases
	assert.NoError(t, removeAll(ufs, ""))
	assert.ErrorIs(t, removeAll(ufs, "nested/."), unix.EINVAL)
	assert.Error(t, removeAll(ufs, "reg.txt/child"))
	assert.NoError(t, removeContents(ufs, ""))
	assert.NoError(t, removeContents(ufs, "missing_parent/child"))
	assert.Error(t, removeContents(ufs, "reg.txt/child"))

	// openFdAt with ".."
	fdFile, err := openFdAt(dirfd, "nested/..")
	require.NoError(t, err)
	_ = fdFile.Close()
	_, err = openFdAt(-1, "missing")
	assert.Error(t, err)

	// Rename branches
	assert.NoError(t, ufs.Rename("reg.txt", "reg.txt"))
	assert.Error(t, ufs.Rename("..", "b"))
	assert.ErrorIs(t, ufs.Rename(".", "b"), ErrBadPathResolution)
	assert.Error(t, ufs.Rename("missing.txt", "b"))
	assert.Error(t, ufs.Rename("reg.txt", ".."))
	assert.ErrorIs(t, ufs.Rename("reg.txt", "."), ErrBadPathResolution)

	f2, err := ufs.Create("reg2.txt")
	require.NoError(t, err)
	_ = f2.Close()
	assert.ErrorIs(t, ufs.Rename("reg.txt", "reg2.txt"), ErrExist)
	assert.NoError(t, ufs.Rename("reg2.txt", "new_rename_parent/reg2_moved.txt"))

	// statFollow, Readlinkat > 256 bytes, Symlink errors, TouchPath errors
	_, err = ufs.statFollow("..")
	assert.Error(t, err)

	longTarget := strings.Repeat("a", 300)
	require.NoError(t, ufs.Symlink(longTarget, "long_sym"))
	target, err := ufs.Readlinkat(dirfd, "long_sym")
	require.NoError(t, err)
	assert.Equal(t, longTarget, target)
	_, err = ufs.Readlinkat(dirfd, "missing_sym")
	assert.Error(t, err)

	assert.Error(t, ufs.Symlink("target", ".."))
	assert.Error(t, ufs.Symlink("target", "reg.txt"))

	_, err = ufs.Touch("..", O_RDWR, 0o644)
	assert.Error(t, err)
	_, err = ufs.Touch("reg.txt/sub/file.txt", O_RDWR, 0o644)
	assert.Error(t, err)

	// checkOpenedType, checkOpenedPath is covered in check_opened_path_linux_test.go
	assert.Error(t, checkOpenedType(-1, "badfd"))
	devNull, err := os.Open("/dev/null")
	require.NoError(t, err)
	defer devNull.Close()
	assert.ErrorIs(t, checkOpenedType(int(devNull.Fd()), "devnull"), ErrNotRegular)
}

func TestStatFollowReportsMissingFilesystemRoot(t *testing.T) {
	root := t.TempDir()
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	require.NoError(t, os.Remove(root))
	_, err = fs.statFollow("file")
	assert.Error(t, err)
	_, _, closeFd, err := fs.SafePath("file")
	closeFd()
	assert.Error(t, err)
}

func TestTouchPathReportsDirectoryCreationFailure(t *testing.T) {
	var root string
	switch runtime.GOOS {
	case "linux":
		root = "/proc"
	case "darwin":
		root = "/System"
	default:
		t.Skip("test requires a read-only system filesystem")
	}
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	_, _, closeFd, err, existed := fs.TouchPath("codex-coverage-missing-parent/child")
	closeFd()
	assert.False(t, existed)
	assert.Error(t, err)
}

func TestRenameReportsMissingParentCreationFailure(t *testing.T) {
	var root, source string
	switch runtime.GOOS {
	case "linux":
		root, source = "/proc", "cpuinfo"
	case "darwin":
		root, source = "/System", "Library/CoreServices/SystemVersion.plist"
	default:
		t.Skip("test requires a read-only system filesystem")
	}
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	err = fs.Rename(source, "codex-coverage-missing-parent/new")
	assert.Error(t, err)
}

func TestRenameReturnsSecondSafePathError(t *testing.T) {
	root := t.TempDir()
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte("x"), 0o600))
	oldMkdirAll := renameMkdirAll
	t.Cleanup(func() { renameMkdirAll = oldMkdirAll })
	renameMkdirAll = func(*UnixFS, string, FileMode) ([]string, error) { return nil, nil }
	err = fs.Rename("source", "missing-parent/destination")
	assert.ErrorIs(t, err, ErrNotExist)
}

func TestRenameWrapsRenameatErrors(t *testing.T) {
	root := t.TempDir()
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte("x"), 0o600))
	oldRenameat := ufsRenameat
	t.Cleanup(func() { ufsRenameat = oldRenameat })
	want := unix.EIO
	ufsRenameat = func(int, string, int, string) error { return want }
	err = fs.Rename("source", "destination")
	require.ErrorIs(t, err, want)
	var linkErr *LinkError
	require.ErrorAs(t, err, &linkErr)
	require.Equal(t, "source", linkErr.Old)
	require.Equal(t, "destination", linkErr.New)
}

func TestRenameReturnsDestinationStatError(t *testing.T) {
	root := t.TempDir()
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte("x"), 0o600))
	oldLstatat := renameLstatat
	t.Cleanup(func() { renameLstatat = oldLstatat })
	want := unix.EIO
	calls := 0
	renameLstatat = func(fs *UnixFS, dirfd int, name string) (FileInfo, error) {
		calls++
		if calls == 2 {
			return nil, want
		}
		return oldLstatat(fs, dirfd, name)
	}
	require.ErrorIs(t, fs.Rename("source", "destination"), want)
}

func TestMkdirAllAcceptsDirectoryCreatedAfterMkdirError(t *testing.T) {
	root := t.TempDir()
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	oldMkdirat := ufsMkdirat
	t.Cleanup(func() { ufsMkdirat = oldMkdirat })
	ufsMkdirat = func(_ int, name string, mode uint32) error {
		if err := os.Mkdir(filepath.Join(root, name), os.FileMode(mode)); err != nil {
			return err
		}
		return unix.EEXIST
	}
	created, err := fs.MkdirAll("created-elsewhere", 0o755)
	require.NoError(t, err)
	require.Empty(t, created)
}

func TestMkdirAllTrimsTrailingSeparators(t *testing.T) {
	root := t.TempDir()
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	created, err := fs.mkdirAll("nested/child///", 0o755)
	require.NoError(t, err)
	require.Equal(t, []string{"nested", "nested/child///"}, created)
}
