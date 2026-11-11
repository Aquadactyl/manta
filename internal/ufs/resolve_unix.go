// SPDX-License-Identifier: MIT

//go:build unix

package ufs

import (
	"strings"

	"golang.org/x/sys/unix"
)

// maxSymlinkHops is the number of symbolic links followed while resolving one
// path before giving up, the same limit Linux applies.
const maxSymlinkHops = 40

// forceUserspaceResolve makes Linux resolve paths with userspaceOpenBeneath
// instead of the kernel's openat2, so that the resolver macOS depends on can be
// held to the same tests. It is only ever set by tests.
var forceUserspaceResolve bool

// userspaceOpenat2 opens name beneath dirfd like openat2 with RESOLVE_BENEATH
// does, for platforms that do not have it.
func (fs *UnixFS) userspaceOpenat2(dirfd int, name string, flag, mode uint64) (int, error) {
	fd, err := userspaceOpenBeneath(dirfd, name, int(flag), uint32(mode))
	if err != nil {
		return -1, ensurePathError(err, "openat2", name)
	}
	return fd, nil
}

// userspaceOpenBeneath opens name relative to dirfd without ever leaving the
// directory dirfd refers to, which is what RESOLVE_BENEATH guarantees on Linux.
//
// The path is walked one component at a time. Every directory is opened without
// following links and held open, so ".." always means the real parent of a
// directory that was walked into, and a symbolic link is read and its target
// resolved the same way, instead of being handed to the kernel. A path that
// would leave dirfd, whether through "..", an absolute path or an absolute link
// target, fails with EXDEV. Because the result of every step is a descriptor and
// never a path, nothing here depends on the length of a path, the case it is
// spelled in, or whether the file system has a name for the file.
func userspaceOpenBeneath(dirfd int, name string, flag int, mode uint32) (int, error) {
	if name == "" {
		return -1, unix.ENOENT
	}
	if name[0] == '/' {
		return -1, unix.EXDEV
	}

	var (
		// owned are the directories opened here, the deepest last. Walking up
		// closes them, and dirfd itself is never closed.
		owned []int
		hops  int
	)
	closeOwned := func() {
		for _, fd := range owned {
			_ = unix.Close(fd)
		}
		owned = nil
	}
	current := func() int {
		if len(owned) == 0 {
			return dirfd
		}
		return owned[len(owned)-1]
	}

	rest := splitComponents(name)
	mustBeDir := strings.HasSuffix(name, "/")
	userNoFollow := flag&unix.O_NOFOLLOW != 0

	// followLink reads the link called comp in the current directory and puts its
	// target in front of the rest of the path. It reports whether comp was one.
	followLink := func(comp string) (bool, error) {
		buf := make([]byte, 4096)
		n, err := unix.Readlinkat(current(), comp, buf)
		if err != nil {
			// EINVAL means it is not a link, the caller reports its own error.
			return false, nil //nolint:nilerr
		}
		if n >= len(buf) {
			return true, unix.ENAMETOOLONG
		}
		if hops++; hops > maxSymlinkHops {
			return true, unix.ELOOP
		}
		target := string(buf[:n])
		if target == "" {
			return true, unix.ENOENT
		}
		if target[0] == '/' {
			return true, unix.EXDEV
		}
		if strings.HasSuffix(target, "/") {
			mustBeDir = true
		}
		rest = append(splitComponents(target), rest...)
		return true, nil
	}

	for len(rest) > 0 {
		comp := rest[0]
		rest = rest[1:]
		last := len(rest) == 0

		if comp == ".." {
			if len(owned) == 0 {
				closeOwned()
				return -1, unix.EXDEV
			}
			_ = unix.Close(owned[len(owned)-1])
			owned = owned[:len(owned)-1]
			continue
		}

		if !last {
			fd, err := openRetry(current(), comp, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err == nil {
				owned = append(owned, fd)
				continue
			}
			// A link is reported differently by each platform, ELOOP or ENOTDIR, so
			// the entry is looked at instead of trusting the error.
			if err == unix.ELOOP || err == unix.ENOTDIR {
				isLink, lerr := followLink(comp)
				if lerr != nil {
					closeOwned()
					return -1, lerr
				}
				if isLink {
					continue
				}
			}
			closeOwned()
			return -1, err
		}

		// The last component is opened with the flags that were asked for. A link
		// is followed unless O_NOFOLLOW was asked for, in which case it is the
		// caller's to see.
		f := flag | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if mustBeDir {
			f |= unix.O_DIRECTORY
		}
		fd, err := openRetry(current(), comp, f, mode)
		if err == nil {
			// A descriptor opened with O_PATH does not fail on a link, it is the
			// link, so look at what was opened.
			if oPath != 0 && flag&oPath != 0 && !userNoFollow {
				var st unix.Stat_t
				if unix.Fstat(fd, &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFLNK {
					_ = unix.Close(fd)
					if isLink, lerr := followLink(comp); lerr != nil {
						closeOwned()
						return -1, lerr
					} else if isLink {
						continue
					}
					closeOwned()
					return -1, unix.ENOENT
				}
			}
			closeOwned()
			return fd, nil
		}
		if !userNoFollow && (err == unix.ELOOP || err == unix.ENOTDIR) {
			isLink, lerr := followLink(comp)
			if lerr != nil {
				closeOwned()
				return -1, lerr
			}
			if isLink {
				continue
			}
		}
		closeOwned()
		return -1, err
	}

	// The path was only ".", or went down and back up again, so it names the
	// directory that is current now.
	fd, err := openRetry(current(), ".", flag|unix.O_CLOEXEC, mode)
	closeOwned()
	if err != nil {
		return -1, err
	}
	return fd, nil
}

// openRetry is openat that tries again when it is interrupted.
func openRetry(dirfd int, name string, flag int, mode uint32) (int, error) {
	for {
		fd, err := unix.Openat(dirfd, name, flag, mode)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

// splitComponents splits a relative path into its components, leaving out the
// empty ones and ".", which do not change where the path leads.
func splitComponents(p string) []string {
	parts := strings.Split(p, "/")
	out := parts[:0]
	for _, c := range parts {
		if c != "" && c != "." {
			out = append(out, c)
		}
	}
	return out
}
