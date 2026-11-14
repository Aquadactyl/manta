//go:build unix

package ufs

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestReadDirRetriesInterruptAndReportsErrors(t *testing.T) {
	fs, err := NewUnixFS(t.TempDir(), false)
	require.NoError(t, err)
	dirfd, _, closeFD, err := fs.SafePath(".")
	require.NoError(t, err)
	defer closeFD()

	original := readDirentCall
	t.Cleanup(func() { readDirentCall = original })
	t.Run("interrupt retries", func(t *testing.T) {
		calls := 0
		readDirentCall = func(fd int, buf []byte) (int, error) {
			calls++
			if calls == 1 {
				return 0, unix.EINTR
			}
			return original(fd, buf)
		}
		entries, err := fs.readDir(dirfd, ".", ".", nil)
		require.NoError(t, err)
		require.Empty(t, entries)
		require.GreaterOrEqual(t, calls, 3) // retry, entries, end of directory
	})

	t.Run("read error is returned", func(t *testing.T) {
		readDirentCall = func(int, []byte) (int, error) { return 0, unix.EIO }
		_, err := fs.readDir(dirfd, "broken", ".", nil)
		require.ErrorIs(t, err, unix.EIO)
		_, err = ReadDirMap(fs, ".", func(d DirEntry) (string, error) { return d.Name(), nil })
		require.ErrorIs(t, err, unix.EIO)
	})
}

func TestReadDirSkipsDeletedEntriesAndReportsUnknownTypes(t *testing.T) {
	fs, err := NewUnixFS(t.TempDir(), false)
	require.NoError(t, err)
	dirfd, _, closeFD, err := fs.SafePath(".")
	require.NoError(t, err)
	defer closeFD()

	original := readDirentCall
	t.Cleanup(func() { readDirentCall = original })
	for _, tc := range []struct {
		name    string
		inode   uint64
		typeVal uint8
		wantErr bool
	}{
		{name: "deleted inode", inode: 0, typeVal: unix.DT_REG},
		{name: "unknown type missing entry", inode: 1, typeVal: unix.DT_UNKNOWN, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := unix.Dirent{Ino: tc.inode, Type: tc.typeVal, Reclen: uint16(unsafe.Sizeof(unix.Dirent{}))}
			copy(unsafe.Slice((*byte)(unsafe.Pointer(&entry.Name[0])), len(entry.Name)), []byte("missing\x00"))
			raw := unsafe.Slice((*byte)(unsafe.Pointer(&entry)), int(unsafe.Sizeof(entry)))
			calls := 0
			readDirentCall = func(_ int, buf []byte) (int, error) {
				calls++
				if calls > 1 {
					return 0, nil
				}
				copy(buf, raw)
				return len(raw), nil
			}
			entries, err := fs.readDir(dirfd, ".", ".", nil)
			if tc.wantErr {
				require.ErrorIs(t, err, ErrNotExist)
				return
			}
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestWalkDirReportsReadErrorToCallback(t *testing.T) {
	fs, err := NewUnixFS(t.TempDir(), false)
	require.NoError(t, err)
	dirfd, _, closeFD, err := fs.SafePath(".")
	require.NoError(t, err)
	defer closeFD()

	original := readDirentCall
	readDirentCall = func(int, []byte) (int, error) { return 0, unix.EIO }
	t.Cleanup(func() { readDirentCall = original })

	t.Run("callback continues", func(t *testing.T) {
		calls := 0
		err := fs.walkDir(nil, dirfd, ".", ".", fakeDirEntry{name: ".", isDir: true}, func(_ int, _, _ string, _ DirEntry, walkErr error) error {
			calls++
			if calls == 2 {
				require.ErrorIs(t, walkErr, unix.EIO)
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 2, calls)
	})

	t.Run("SkipDir consumes read error", func(t *testing.T) {
		calls := 0
		err := fs.walkDir(nil, dirfd, ".", ".", fakeDirEntry{name: ".", isDir: true}, func(_ int, _, _ string, _ DirEntry, walkErr error) error {
			calls++
			if calls == 2 {
				require.ErrorIs(t, walkErr, unix.EIO)
				return SkipDir
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 2, calls)
	})

	t.Run("callback error is returned", func(t *testing.T) {
		want := errors.New("callback failed")
		calls := 0
		err := fs.walkDir(nil, dirfd, ".", ".", fakeDirEntry{name: ".", isDir: true}, func(_ int, _, _ string, _ DirEntry, _ error) error {
			calls++
			if calls == 2 {
				return want
			}
			return nil
		})
		require.ErrorIs(t, err, want)
	})
}
