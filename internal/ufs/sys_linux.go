// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright (c) 2024 Matthew Penner

//go:build linux

package ufs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

var (
	linuxFstat        = unix.Fstat
	linuxEvalSymlinks = filepath.EvalSymlinks
)

var ufsOpenat2 = unix.Openat2

const (
	// O_LARGEFILE allows files larger than 2GiB to be opened on 32-bit systems.
	O_LARGEFILE = unix.O_LARGEFILE

	// AT_EMPTY_PATH is passed in place of a directory file descriptor when the
	// path given is absolute and the descriptor is therefore ignored.
	AT_EMPTY_PATH = unix.AT_EMPTY_PATH
)

var statOpenedPath = unix.Fstat

var openedPathIsSymlink = func(fd, flag int, userNoFollow bool) bool {
	if flag&oPath == 0 || userNoFollow {
		return false
	}
	var st unix.Stat_t
	return statOpenedPath(fd, &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFLNK
}

const (
	// oPath opens a path without reading from or writing to the file.
	oPath = unix.O_PATH

	// utimeOmit leaves a timestamp unchanged when passed to utimensat.
	utimeOmit = unix.UTIME_OMIT
)

// normalizeOpenat2 returns whether paths are resolved with openat2. On Linux
// that is whatever was asked for, apart from tests that exercise the resolver of
// other platforms.
func normalizeOpenat2(use bool) bool {
	return use || forceUserspaceResolve
}

// openBeneath opens name beneath dirfd without leaving it.
func (fs *UnixFS) openBeneath(dirfd int, name string, flag, mode uint64) (int, error) {
	if forceUserspaceResolve {
		return fs.userspaceOpenat2(dirfd, name, flag, mode)
	}
	return fs._openat2(dirfd, name, flag, mode)
}

// minimumScratchBufferSize is the smallest buffer used to read directory
// entries.
var minimumScratchBufferSize = os.Getpagesize()

// readDirent reads directory entries from fd into buf.
func readDirent(fd int, buf []byte) (int, error) {
	return unix.Getdents(fd, buf)
}

// fchmodatNoFollow changes the mode of name relative to dirfd without following
// a symbolic link in the last element.
func fchmodatNoFollow(dirfd int, name string, mode uint32) error {
	err := unix.Fchmodat(dirfd, name, mode, AT_SYMLINK_NOFOLLOW)
	if err == unix.EOPNOTSUPP {
		// Kernels before 6.6 do not support fchmodat2, which is needed to pass
		// AT_SYMLINK_NOFOLLOW.
		err = fchmodNoFollow(dirfd, name, mode)
	}
	return err
}

// fchmodNoFollow changes the mode of name without following it if it is a
// symbolic link, without needing fchmodat2.
func fchmodNoFollow(dirfd int, name string, mode uint32) error {
	fd, err := unix.Openat(dirfd, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	var st unix.Stat_t
	if err := linuxFstat(fd, &st); err != nil {
		return err
	}
	// Linux does not support changing the mode of a symbolic link.
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return unix.EOPNOTSUPP
	}
	// The descriptor was opened with O_PATH, which fchmod does not accept, so
	// change the mode through its entry in /proc, which refers to the file that
	// was opened.
	return unix.Chmod("/proc/self/fd/"+strconv.Itoa(fd), mode)
}

// statResolved describes the file called rel beneath rootfd, following a
// symbolic link in the last element as long as it resolves to a path beneath
// rootfd. The name is only used to report errors.
func (fs *UnixFS) statResolved(rootfd int, rel, name string, st *unix.Stat_t) error {
	// O_PATH gives a descriptor that can be described, but never read from or
	// written to, so the type of the file does not need to be checked.
	fd, err := fs.openatResolvedType(rootfd, rel, oPath, 0, false)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	if err := ignoringEINTR(func() error {
		return linuxFstat(fd, st)
	}); err != nil {
		return ensurePathError(err, "stat", name)
	}
	return nil
}

// checkOpenedPath returns an error if the file opened as fd, without the
// protection of openat2, resolved to a path outside of the filesystem.
func (fs *UnixFS) checkOpenedPath(fd int, name string) error {
	var finalPath string
	finalPath, err := linuxEvalSymlinks(filepath.Join("/proc/self/fd/", strconv.Itoa(fd)))
	if err != nil {
		if !errors.Is(err, ErrNotExist) {
			return fmt.Errorf("failed to evaluate symlink: %w", convertErrorType(err))
		}

		// The target of one of the symlinks (EvalSymlinks is recursive)
		// does not exist. So get the path that does not exist and use
		// that for further validation instead.
		var pErr *PathError
		if !errors.As(err, &pErr) {
			return fmt.Errorf("failed to evaluate symlink: %w", convertErrorType(err))
		}

		// Update the final path to whatever directory or path didn't exist while
		// recursing any symlinks.
		finalPath = pErr.Path
		// Ensure the error is wrapped correctly.
		err = convertErrorType(err)
	}

	// Check if the path is within our root.
	if !fs.unsafeIsPathInsideOfBase(finalPath) {
		op := "openat"
		if fs.useOpenat2 {
			op = "openat2"
		}
		return &PathError{
			Op:   op,
			Path: name,
			Err:  ErrBadPathResolution,
		}
	}

	return err
}

// _openat2 is a wonderful syscall that supersedes the `openat` syscall. It has
// improved validation and security characteristics that weren't available or
// considered when `openat` was originally implemented. As such, it is only
// present in Kernel 5.6 and above.
//
// This method should never be directly called, use `openat` instead.
func (fs *UnixFS) _openat2(dirfd int, name string, flag, mode uint64) (int, error) {
	// Ensure the O_CLOEXEC flag is set.
	// Go sets this when using the os package, but since we are directly using
	// the unix package we need to set it ourselves.
	if flag&O_CLOEXEC == 0 {
		flag |= O_CLOEXEC
	}
	// Ensure the O_LARGEFILE flag is set.
	// Go sets this for unix.Open, unix.Openat, but not unix.Openat2.
	if flag&O_LARGEFILE == 0 {
		flag |= O_LARGEFILE
	}
	fd, err := ufsOpenat2(dirfd, name, &unix.OpenHow{
		Flags: flag,
		Mode:  mode,
		// This is the bread and butter of preventing a symlink escape, without
		// this option, we have to handle path validation fully on our own.
		//
		// This is why using Openat2 over Openat is preferred if available.
		Resolve: unix.RESOLVE_BENEATH,
	})
	switch err {
	case nil:
		return fd, nil
	case unix.EINTR:
		return fd, err
	case unix.EAGAIN:
		return fd, err
	default:
		return fd, ensurePathError(err, "openat2", name)
	}
}
