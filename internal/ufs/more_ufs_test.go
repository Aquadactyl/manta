package ufs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestUfsErrors(t *testing.T) {
	err := NewSyscallError("openat", unix.ENOENT)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "openat")

	// convertErrorType
	pe := convertErrorType(&os.PathError{Op: "open", Path: "file", Err: unix.ENOENT})
	assert.NotNil(t, pe)

	pe2 := convertErrorType(&PathError{Op: "open", Path: "file", Err: unix.ENOENT})
	assert.NotNil(t, pe2)

	assert.Nil(t, convertErrorType(nil))
	assert.NotNil(t, convertErrorType(errors.New("generic")))
}

func TestWalkDirAndReadDirMap(t *testing.T) {
	root := t.TempDir()
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	defer fs.Close()

	require.NoError(t, os.MkdirAll(filepath.Join(root, "subdir", "nested"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "f1.txt"), []byte("file1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "subdir", "f2.txt"), []byte("file2"), 0644))

	// WalkDir
	var visited []string
	err = fs.WalkDir(".", func(path string, d DirEntry, err error) error {
		require.NoError(t, err)
		visited = append(visited, path)
		if path == "subdir/nested" {
			return SkipDir
		}
		return nil
	})
	require.NoError(t, err)
	assert.Contains(t, visited, "f1.txt")
	assert.Contains(t, visited, "subdir")
	assert.Contains(t, visited, "subdir/f2.txt")

	// ReadDirMap
	names, err := ReadDirMap(fs, ".", func(de DirEntry) (string, error) {
		assert.NotEmpty(t, de.Name())

		info, err := de.Info()
		assert.NoError(t, err)
		assert.NotNil(t, info)

		if !de.IsDir() {
			f, err := fs.Open(de.Name())
			assert.NoError(t, err)
			if f != nil {
				buf, _ := io.ReadAll(f)
				assert.NotEmpty(t, buf)
				_ = f.Close()
			}
		}

		return de.Name(), nil
	})
	require.NoError(t, err)
	assert.Contains(t, names, "f1.txt")
	assert.Contains(t, names, "subdir")
}

func TestQuotaRemoveAndRemoveAll(t *testing.T) {
	root := t.TempDir()
	unixFs, err := NewUnixFS(root, false)
	require.NoError(t, err)

	quota := NewQuota(unixFs, 1024*1024)

	// Create test files
	require.NoError(t, os.MkdirAll(filepath.Join(root, "qdir", "sub"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "qdir", "sub", "test.txt"), []byte("hello"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "single.txt"), []byte("world"), 0644))

	// Set initial usage
	quota.SetUsage(100)

	// Remove single file
	err = quota.Remove("single.txt")
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(root, "single.txt"))

	// Remove empty directory
	emptyDir := filepath.Join(root, "empty")
	require.NoError(t, os.MkdirAll(emptyDir, 0755))
	err = quota.Remove("empty")
	require.NoError(t, err)

	// RemoveAll on subdirectory
	err = quota.RemoveAll("qdir")
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(root, "qdir"))

	// RemoveAll on "." returns ErrBadPathResolution
	err = quota.RemoveAll(".")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrBadPathResolution))

	// Close
	assert.NoError(t, quota.Close())
}

func TestUnixFSRemoveStatAndRemoveContents(t *testing.T) {
	root := t.TempDir()
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	defer fs.Close()

	// RemoveStat file
	require.NoError(t, os.WriteFile(filepath.Join(root, "stat_file.txt"), []byte("content"), 0644))
	fi, err := fs.RemoveStat("stat_file.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(7), fi.Size())

	// RemoveStat dir
	require.NoError(t, os.MkdirAll(filepath.Join(root, "empty_dir"), 0755))
	fi, err = fs.RemoveStat("empty_dir")
	require.NoError(t, err)
	assert.True(t, fi.IsDir())

	// RemoveContents
	require.NoError(t, os.MkdirAll(filepath.Join(root, "parent", "c1"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "parent", "c2.txt"), []byte("hello"), 0644))
	err = fs.RemoveContents("parent")
	require.NoError(t, err)
	assert.DirExists(t, filepath.Join(root, "parent"))
	assert.NoFileExists(t, filepath.Join(root, "parent", "c2.txt"))
	assert.NoDirExists(t, filepath.Join(root, "parent", "c1"))
}

func TestUnixFSOperations(t *testing.T) {
	root := t.TempDir()
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	defer fs.Close()

	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("data"), 0644))
	require.NoError(t, os.Symlink("file.txt", filepath.Join(root, "link.txt")))

	// Chown / Lchown (current user)
	uid := os.Getuid()
	gid := os.Getgid()
	assert.NoError(t, fs.Chown("file.txt", uid, gid))
	assert.NoError(t, fs.Lchown("link.txt", uid, gid))

	// Mkdirat / Chmodat / Readlinkat
	dirfd, _, closeFd, err := fs.SafePath(".")
	require.NoError(t, err)
	defer closeFd()

	assert.NoError(t, fs.Mkdirat(dirfd, "new_at_dir", 0755))
	assert.NoError(t, fs.Chmodat(dirfd, "new_at_dir", 0750))

	target, err := fs.Readlinkat(dirfd, "link.txt")
	require.NoError(t, err)
	assert.Equal(t, "file.txt", target)

	// Chownat / Lchownat
	assert.NoError(t, fs.Chownat(dirfd, "file.txt", uid, gid))
	assert.NoError(t, fs.Lchownat(dirfd, "link.txt", uid, gid))
}

func TestUnixFSRenameAndModeTypes(t *testing.T) {
	root := t.TempDir()
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	defer fs.Close()

	// 1. Rename: same path returns nil
	assert.NoError(t, fs.Rename("same.txt", "same.txt"))

	// 2. Rename: oldname "." returns ErrBadPathResolution
	err = fs.Rename(".", "other")
	assert.Error(t, err)

	// 3. Rename file to file
	require.NoError(t, os.WriteFile(filepath.Join(root, "src.txt"), []byte("content"), 0644))
	require.NoError(t, fs.Rename("src.txt", "dst.txt"))
	assert.FileExists(t, filepath.Join(root, "dst.txt"))
	assert.NoFileExists(t, filepath.Join(root, "src.txt"))

	// 4. Rename to nested destination (creates missing parent directory)
	require.NoError(t, fs.Rename("dst.txt", "sub/folder/nested.txt"))
	assert.FileExists(t, filepath.Join(root, "sub/folder/nested.txt"))

	// 5. ReadDirMap and dirent.Open()
	files, err := ReadDirMap(fs, "sub/folder", func(de DirEntry) (File, error) {
		if opener, ok := de.(interface{ Open() (File, error) }); ok {
			return opener.Open()
		}
		return nil, nil
	})
	require.NoError(t, err)
	assert.NotEmpty(t, files)
	for _, f := range files {
		if f != nil {
			_ = f.Close()
		}
	}

	// 6. modeTypeFromDirent for each dirent type
	dirfd, _, closeFd, err := fs.SafePath(".")
	require.NoError(t, err)
	defer closeFd()

	types := []struct {
		deType uint8
		want   FileMode
	}{
		{unix.DT_REG, 0},
		{unix.DT_DIR, ModeDir},
		{unix.DT_LNK, ModeSymlink},
		{unix.DT_CHR, ModeDevice | ModeCharDevice},
		{unix.DT_BLK, ModeDevice},
		{unix.DT_FIFO, ModeNamedPipe},
		{unix.DT_SOCK, ModeSocket},
	}
	for _, tt := range types {
		mode, err := fs.modeTypeFromDirent(&unix.Dirent{Type: tt.deType}, dirfd, "test")
		assert.NoError(t, err)
		assert.Equal(t, tt.want, mode)
	}

	// DT_UNKNOWN fallback to modeType(dirfd, name)
	require.NoError(t, os.WriteFile(filepath.Join(root, "known.txt"), []byte("x"), 0644))
	mode, err := fs.modeTypeFromDirent(&unix.Dirent{Type: unix.DT_UNKNOWN}, dirfd, "known.txt")
	assert.NoError(t, err)
	assert.Equal(t, FileMode(0), mode)
}

func TestPathHelpers(t *testing.T) {
	assert.Equal(t, "c", basename("/a/b/c/"))
	assert.Equal(t, "file.txt", basename("file.txt"))
	assert.Equal(t, "/", basename("/"))

	assert.True(t, endsWithDot("."))
	assert.True(t, endsWithDot("/a/b/."))
	assert.False(t, endsWithDot("/a/b/.."))
	assert.False(t, endsWithDot("/a/b/c"))

	dir, base := splitPath("//a/b/c//")
	assert.Equal(t, "c", base)
	assert.Equal(t, "/a/b", dir)

	dir, base = splitPath("single")
	assert.Equal(t, "single", base)
	assert.Equal(t, ".", dir)
}

func TestErrorConversionHelpers(t *testing.T) {
	assert.Nil(t, ensurePathError(nil, "op", "path"))
	assert.NotNil(t, ensurePathError(errors.New("other"), "op", "path"))

	errnos := []syscall.Errno{
		unix.EEXIST,
		unix.ENOENT,
		unix.EISDIR,
		unix.ENOTDIR,
		unix.EPERM,
		unix.EACCES,
		unix.ENOSPC,
		unix.EXDEV,
		unix.ENOTEMPTY,
		syscall.Errno(9999),
	}

	for _, errno := range errnos {
		pe := errnoToPathError(errno, "test-op", "test-path")
		assert.NotNil(t, pe)

		ensured := ensurePathError(errno, "test-op", "test-path")
		assert.NotNil(t, ensured)

		converted := convertErrorType(errno)
		assert.NotNil(t, converted)
	}

	// PathError wrapping errno
	wrapped := &PathError{Op: "op", Path: "path", Err: unix.ENOENT}
	assert.NotNil(t, ensurePathError(wrapped, "ignored", "ignored"))
	assert.NotNil(t, convertErrorType(wrapped))

	// PathError wrapping non-errno
	wrappedGeneric := &PathError{Op: "op", Path: "path", Err: errors.New("generic")}
	assert.NotNil(t, ensurePathError(wrappedGeneric, "ignored", "ignored"))
	assert.NotNil(t, convertErrorType(wrappedGeneric))
}

func TestQuotaWriterAndReader(t *testing.T) {
	qw := &CountedWriter{err: errors.New("custom write err")}
	assert.Equal(t, "custom write err", qw.Error().Error())

	qr := &CountedReader{err: errors.New("custom read err")}
	assert.Equal(t, "custom read err", qr.Error().Error())
}

func TestUnixFSAdditionalOps(t *testing.T) {
	root := t.TempDir()
	fs, err := NewUnixFS(root, false)
	require.NoError(t, err)
	defer fs.Close()

	// TouchPath on nested non-existent directory -> exists is false
	dirfd, name, closeFd, err, exists := fs.TouchPath("nested/file.txt")
	assert.NoError(t, err)
	assert.False(t, exists)
	assert.NotEmpty(t, name)
	if closeFd != nil {
		closeFd()
	}
	_ = dirfd

	// Create file directly
	require.NoError(t, os.WriteFile(filepath.Join(root, "touched.txt"), []byte("hello"), 0644))

	// TouchPath on existing path
	dirfd, name, closeFd, err, exists = fs.TouchPath("touched.txt")
	assert.NoError(t, err)
	assert.True(t, exists)
	if closeFd != nil {
		closeFd()
	}

	// Chmod
	require.NoError(t, fs.Chmod("touched.txt", 0644))

	// Chtimes
	now := time.Now().Add(-time.Hour)
	require.NoError(t, fs.Chtimes("touched.txt", now, now))

	// Symlink
	require.NoError(t, fs.Symlink("touched.txt", "sym.txt"))
	assert.FileExists(t, filepath.Join(root, "sym.txt"))

	// RemoveContents
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dir_to_empty", "inner"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dir_to_empty", "file.txt"), []byte("data"), 0644))
	require.NoError(t, fs.RemoveContents("dir_to_empty"))
	assert.DirExists(t, filepath.Join(root, "dir_to_empty"))
	entries, err := os.ReadDir(filepath.Join(root, "dir_to_empty"))
	assert.NoError(t, err)
	assert.Empty(t, entries)
}

