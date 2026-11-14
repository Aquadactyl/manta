// SPDX-License-Identifier: MIT

//go:build darwin

package ufs

import (
	"errors"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

// macOS has no openat2 and no /proc. Paths are instead resolved by
// userspaceOpenBeneath, which walks them one component at a time and never lets
// the kernel follow a link, so nothing outside of the base can be reached, and
// nothing depends on the length or the spelling of a path.

const (
	// O_LARGEFILE does not exist on macOS, file offsets are always 64-bit.
	O_LARGEFILE = 0

	// AT_EMPTY_PATH does not exist on macOS. The value is only ever passed as
	// the directory descriptor of an absolute path, which ignores it, so use the
	// descriptor that means "the current directory".
	AT_EMPTY_PATH = unix.AT_FDCWD
)

var openedPathIsSymlink = func(_ int, _ int, _ bool) bool { return false }

const (
	// oPath is zero as macOS has no O_PATH. Descriptors that are only described
	// are opened by statResolved instead.
	oPath = 0

	// utimeOmit leaves a timestamp unchanged when passed to utimensat, the value
	// of UTIME_OMIT in <sys/stat.h>.
	utimeOmit = -2
)

var statResolvedFstat = unix.Fstat

// minimumScratchBufferSize is the smallest buffer used to read directory
// entries. getdirentries(2) fails if the buffer is smaller than the block size
// of the filesystem, so be generous.
var minimumScratchBufferSize = max(os.Getpagesize(), 64*1024)

// normalizeOpenat2 returns whether paths are resolved beneath a directory by
// UnixFS itself, which is always the case on macOS.
func normalizeOpenat2(_ bool) bool {
	return true
}

// openBeneath opens name beneath dirfd without leaving it.
func (fs *UnixFS) openBeneath(dirfd int, name string, flag, mode uint64) (int, error) {
	return fs.userspaceOpenat2(dirfd, name, flag, mode)
}

// checkOpenedPath is never reached, as paths are always resolved beneath their
// directory and there is nothing left to verify afterwards.
func (fs *UnixFS) checkOpenedPath(_ int, name string) error {
	return ensurePathError(errors.New("path verification after opening is not used on macOS"), "openat", name)
}

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
			return statResolvedFstat(fd, st)
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
	// could lead outside of the base. The parent directory is resolved like any
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
