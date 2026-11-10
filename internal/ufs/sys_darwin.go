// SPDX-License-Identifier: MIT

//go:build darwin

package ufs

import (
	"bytes"
	"errors"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

// macOS has no openat2 and no /proc. Containment of every path is instead
// enforced by opening the path with openat and asking the kernel for the
// canonical path of the resulting descriptor (fcntl F_GETPATH), which is then
// required to be beneath the canonical path of the filesystem's base. The
// parent directory of every operation is verified this way before anything is
// created, so nothing outside of the base can be written to.

const (
	// O_LARGEFILE does not exist on macOS, file offsets are always 64-bit.
	O_LARGEFILE = 0

	// AT_EMPTY_PATH does not exist on macOS. The value is only ever passed as
	// the directory descriptor of an absolute path, which ignores it, so use the
	// descriptor that means "the current directory".
	AT_EMPTY_PATH = unix.AT_FDCWD
)

const (
	// oPath is zero as macOS has no O_PATH. Descriptors that are only described
	// are opened by statResolved instead.
	oPath = 0

	// utimeOmit leaves a timestamp unchanged when passed to utimensat, the value
	// of UTIME_OMIT in <sys/stat.h>.
	utimeOmit = -2

	// openat2Available reports whether the openat2 syscall can exist on this
	// platform.
	openat2Available = false
)

// platformState holds the state UnixFS needs on top of its common fields.
type platformState struct {
	// realBasePath caches the canonical path of the base path, see realBase.
	realBasePath atomic.Pointer[string]
}

// minimumScratchBufferSize is the smallest buffer used to read directory
// entries. getdirentries(2) fails if the buffer is smaller than the block size
// of the filesystem, so be generous.
var minimumScratchBufferSize = max(os.Getpagesize(), 64*1024)

// readDirent reads directory entries from fd into buf.
func readDirent(fd int, buf []byte) (int, error) {
	return unix.ReadDirent(fd, buf)
}

// fchmodatNoFollow changes the mode of name relative to dirfd without following
// a symbolic link in the last element.
func fchmodatNoFollow(dirfd int, name string, mode uint32) error {
	return fchmodNoFollow(dirfd, name, mode)
}

// fchmodNoFollow changes the mode of name without following it if it is a
// symbolic link. macOS supports this natively through fchmodat(2).
func fchmodNoFollow(dirfd int, name string, mode uint32) error {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	// Changing the mode of a symbolic link is not supported on Linux, so it is
	// refused here as well to behave the same on every platform. If the entry is
	// swapped for a symbolic link after the check, AT_SYMLINK_NOFOLLOW still
	// ensures the link itself is changed and never its target.
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return unix.EOPNOTSUPP
	}
	return unix.Fchmodat(dirfd, name, mode, AT_SYMLINK_NOFOLLOW)
}

// statResolved describes the file called rel beneath rootfd, following a
// symbolic link in the last element as long as it resolves to a path beneath
// rootfd. The name is only used to report errors.
func (fs *UnixFS) statResolved(rootfd int, rel, name string, st *unix.Stat_t) error {
	// There is no O_PATH on macOS, so the file is opened for reading without
	// blocking (so that a named pipe can not stall the daemon) and without
	// becoming a controlling terminal. It is never read from, so the type of the
	// file does not need to be checked.
	fd, err := fs.openatResolvedType(rootfd, rel, O_RDONLY|unix.O_NONBLOCK|unix.O_NOCTTY, 0, false)
	if err == nil {
		defer unix.Close(fd)
		if err := ignoringEINTR(func() error {
			return unix.Fstat(fd, st)
		}); err != nil {
			return ensurePathError(err, "stat", name)
		}
		return nil
	}
	if !errors.Is(err, ErrPermission) {
		return err
	}

	// The file exists but can not be opened for reading. It can still be
	// described when it is not a symbolic link, as nothing is followed that
	// could lead outside of the base. The parent directory is verified like any
	// other path before the entry is looked at.
	dir, base := path.Split(rel)
	if dir == "" {
		dir = "."
	}
	dirfd, derr := fs.openat(rootfd, strings.TrimSuffix(dir, "/"), O_DIRECTORY|O_RDONLY, 0)
	if derr != nil {
		return err
	}
	defer unix.Close(dirfd)

	if serr := ignoringEINTR(func() error {
		return unix.Fstatat(dirfd, base, st, AT_SYMLINK_NOFOLLOW)
	}); serr != nil || st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return err
	}
	return nil
}

// checkOpenedPath returns an error if the file opened as fd, without the
// protection of openat2, resolved to a path outside of the filesystem.
func (fs *UnixFS) checkOpenedPath(fd int, name string) error {
	base, err := fs.realBase()
	if err != nil {
		return ensurePathError(err, "openat", name)
	}
	// Failing to resolve the path, for example because the file was removed
	// after it was opened, is treated as a failure, never as success.
	finalPath, err := fdPath(fd)
	if err != nil {
		return ensurePathError(err, "openat", name)
	}
	if !isPathInside(finalPath, base) {
		return &PathError{
			Op:   "openat",
			Path: name,
			Err:  ErrBadPathResolution,
		}
	}
	return nil
}

// realBase returns the canonical path of the filesystem's base path, which is
// what fdPath reports for descriptors beneath it. It differs from the base path
// when the base path contains symbolic links, which is the case for the common
// locations /tmp, /var and /etc as they are links into /private on macOS.
func (fs *UnixFS) realBase() (string, error) {
	if p := fs.realBasePath.Load(); p != nil {
		return *p, nil
	}
	fd, err := fs._openat(AT_EMPTY_PATH, fs.basePath, O_DIRECTORY|O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer unix.Close(fd)

	p, err := fdPath(fd)
	if err != nil {
		return "", err
	}
	p = strings.TrimSuffix(p, "/")
	fs.realBasePath.Store(&p)
	return p, nil
}

// isPathInside reports whether p is base or a path beneath it. The comparison
// is exact, so it is deliberately case-sensitive even on case-insensitive
// volumes: both sides come from the kernel, and a mismatch only ever fails
// closed.
func isPathInside(p, base string) bool {
	return strings.HasPrefix(strings.TrimSuffix(p, "/")+"/", base+"/")
}

// fdPath returns the canonical path of the file open as fd, with symbolic links
// resolved. It fails for paths longer than PATH_MAX and for files that no longer
// have a path.
func fdPath(fd int) (string, error) {
	var buf [unix.PathMax]byte
	if _, _, errno := unix.Syscall(unix.SYS_FCNTL, uintptr(fd), uintptr(unix.F_GETPATH), uintptr(unsafe.Pointer(&buf[0]))); errno != 0 { //nolint:gosec // F_GETPATH writes the path into the PATH_MAX sized buffer
		return "", errno
	}
	n := bytes.IndexByte(buf[:], 0)
	if n < 0 {
		n = len(buf)
	}
	return string(buf[:n]), nil
}

// _openat2 is never used on macOS as UnixFS never enables openat2 there.
func (fs *UnixFS) _openat2(_ int, name string, _, _ uint64) (int, error) {
	return -1, ensurePathError(unix.ENOSYS, "openat2", name)
}
