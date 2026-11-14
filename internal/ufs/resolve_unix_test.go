//go:build unix

package ufs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// openFDs returns the number of descriptors this process holds.
func openFDs(t *testing.T) int {
	t.Helper()

	var rl unix.Rlimit
	require.NoError(t, unix.Getrlimit(unix.RLIMIT_NOFILE, &rl))
	n := 0
	for fd := 0; uint64(fd) < min(uint64(rl.Cur), 4096); fd++ {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			n++
		}
	}
	return n
}

// layout builds a directory with a base inside of it and a secret next to the
// base, so a path that leaves the base is told apart from one that does not.
func layout(t *testing.T) (base string, dirfd int) {
	t.Helper()

	top := t.TempDir()
	base = filepath.Join(top, "base")
	require.NoError(t, os.MkdirAll(filepath.Join(base, "a", "b"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "shared"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(base, "a", "b", "file.txt"), []byte("inside"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(base, "shared", "data.txt"), []byte("shared"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(top, "secret.txt"), []byte("secret"), 0o644))

	fd, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = unix.Close(fd) })
	return base, fd
}

func readFD(t *testing.T, fd int) string {
	t.Helper()
	defer unix.Close(fd)

	buf := make([]byte, 64)
	n, err := unix.Read(fd, buf)
	require.NoError(t, err)
	return string(buf[:n])
}

func TestUserspaceOpenBeneathResolvesInsideTheBase(t *testing.T) {
	base, dirfd := layout(t)
	require.NoError(t, os.Symlink("shared/data.txt", filepath.Join(base, "rel-link")))
	require.NoError(t, os.Symlink("../shared", filepath.Join(base, "a", "up-link")))
	require.NoError(t, os.Symlink("b", filepath.Join(base, "a", "dir-link")))
	require.NoError(t, os.Symlink("rel-link", filepath.Join(base, "chain")))

	for _, tt := range []struct {
		name, path, want string
	}{
		{"file", "a/b/file.txt", "inside"},
		{"dot components", "./a/./b//file.txt", "inside"},
		{"dotdot staying inside", "a/b/../b/file.txt", "inside"},
		{"relative link to a file", "rel-link", "shared"},
		{"link to a directory", "a/dir-link/file.txt", "inside"},
		{"link going up and back down", "a/up-link/data.txt", "shared"},
		{"chain of links", "chain", "shared"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fd, err := userspaceOpenBeneath(dirfd, tt.path, unix.O_RDONLY, 0)
			require.NoError(t, err)
			assert.Equal(t, tt.want, readFD(t, fd))
		})
	}
}

func TestUserspaceOpenBeneathRefusesToLeaveTheBase(t *testing.T) {
	base, dirfd := layout(t)
	top := filepath.Dir(base)
	require.NoError(t, os.Symlink(top, filepath.Join(base, "abs-dir")))
	require.NoError(t, os.Symlink(filepath.Join(top, "secret.txt"), filepath.Join(base, "abs-file")))
	require.NoError(t, os.Symlink("../secret.txt", filepath.Join(base, "rel-out")))
	require.NoError(t, os.Symlink("../../secret.txt", filepath.Join(base, "a", "deep-out")))
	require.NoError(t, os.Symlink("a/../../secret.txt", filepath.Join(base, "sneaky")))

	for _, tt := range []struct{ name, path string }{
		{"absolute path", filepath.Join(top, "secret.txt")},
		{"dotdot", "../secret.txt"},
		{"dotdot after a directory", "a/../../secret.txt"},
		{"dotdot only", ".."},
		{"absolute link to a directory", "abs-dir/secret.txt"},
		{"absolute link to a file", "abs-file"},
		{"relative link out", "rel-out"},
		{"link out from a subdirectory", "a/deep-out"},
		{"link that goes in and out", "sneaky"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fd, err := userspaceOpenBeneath(dirfd, tt.path, unix.O_RDONLY, 0)
			if err == nil {
				_ = unix.Close(fd)
			}
			assert.ErrorIs(t, err, unix.EXDEV)
		})
	}
}

func TestUserspaceOpenBeneathDirectoriesAndNames(t *testing.T) {
	_, dirfd := layout(t)

	// The directory itself, in each of the ways it can be spelled.
	for _, p := range []string{".", "a/..", "./", "a/b/../.."} {
		fd, err := userspaceOpenBeneath(dirfd, p, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		require.NoError(t, err, p)
		_ = unix.Close(fd)
	}

	// A trailing slash demands a directory.
	fd, err := userspaceOpenBeneath(dirfd, "a/b/file.txt/", unix.O_RDONLY, 0)
	if err == nil {
		_ = unix.Close(fd)
	}
	assert.ErrorIs(t, err, unix.ENOTDIR)
	fd, err = userspaceOpenBeneath(dirfd, "a/b/", unix.O_RDONLY, 0)
	require.NoError(t, err)
	_ = unix.Close(fd)

	_, err = userspaceOpenBeneath(dirfd, "", unix.O_RDONLY, 0)
	assert.ErrorIs(t, err, unix.ENOENT)
	_, err = userspaceOpenBeneath(dirfd, "missing/file.txt", unix.O_RDONLY, 0)
	assert.ErrorIs(t, err, unix.ENOENT)
	_, err = userspaceOpenBeneath(dirfd, "a/b/file.txt/more", unix.O_RDONLY, 0)
	assert.ErrorIs(t, err, unix.ENOTDIR)
}

func TestUserspaceOpenBeneathLoops(t *testing.T) {
	base, dirfd := layout(t)
	require.NoError(t, os.Symlink("loop-b", filepath.Join(base, "loop-a")))
	require.NoError(t, os.Symlink("loop-a", filepath.Join(base, "loop-b")))
	require.NoError(t, os.Symlink("self", filepath.Join(base, "self")))

	for _, p := range []string{"loop-a", "self", "loop-a/file"} {
		_, err := userspaceOpenBeneath(dirfd, p, unix.O_RDONLY, 0)
		assert.ErrorIs(t, err, unix.ELOOP, p)
	}
}

func TestUserspaceOpenBeneathNoFollow(t *testing.T) {
	base, dirfd := layout(t)
	require.NoError(t, os.Symlink("shared/data.txt", filepath.Join(base, "link")))

	// A link at the end is the caller's to see when it asked not to follow it.
	_, err := userspaceOpenBeneath(dirfd, "link", unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	assert.ErrorIs(t, err, unix.ELOOP)

	// A link in the middle is followed regardless.
	require.NoError(t, os.Symlink("shared", filepath.Join(base, "dir-link")))
	fd, err := userspaceOpenBeneath(dirfd, "dir-link/data.txt", unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	require.NoError(t, err)
	assert.Equal(t, "shared", readFD(t, fd))
}

func TestUserspaceOpenBeneathOPathFollowsFinalSymlink(t *testing.T) {
	base, dirfd := layout(t)
	require.NoError(t, os.Symlink("shared/data.txt", filepath.Join(base, "opath-link")))

	fd, err := userspaceOpenBeneath(dirfd, "opath-link", oPath, 0)
	require.NoError(t, err)
	defer unix.Close(fd)
	var st unix.Stat_t
	require.NoError(t, unix.Fstat(fd, &st))
	assert.Equal(t, uint32(unix.S_IFREG), st.Mode&unix.S_IFMT)
}

// Creating a file through a link that points at nothing creates it where the
// link points, as long as that is inside the base.
func TestUserspaceOpenBeneathCreatesThroughLinks(t *testing.T) {
	base, dirfd := layout(t)
	require.NoError(t, os.Symlink("shared/new.txt", filepath.Join(base, "dangling")))
	require.NoError(t, os.Symlink("../outside-new.txt", filepath.Join(base, "dangling-out")))

	fd, err := userspaceOpenBeneath(dirfd, "dangling", unix.O_WRONLY|unix.O_CREAT, 0o644)
	require.NoError(t, err)
	_ = unix.Close(fd)
	_, err = os.Stat(filepath.Join(base, "shared", "new.txt"))
	assert.NoError(t, err)

	// A link that points out of the base is never followed to create anything.
	_, err = userspaceOpenBeneath(dirfd, "dangling-out", unix.O_WRONLY|unix.O_CREAT, 0o644)
	assert.ErrorIs(t, err, unix.EXDEV)
	_, err = os.Stat(filepath.Join(filepath.Dir(base), "outside-new.txt"))
	assert.ErrorIs(t, err, os.ErrNotExist)

	// Exclusive creation does not follow links, so it fails on one.
	_, err = userspaceOpenBeneath(dirfd, "dangling", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0o644)
	assert.ErrorIs(t, err, unix.EEXIST)
}

// A path far longer than PATH_MAX is reachable, as every step is one component.
func TestUserspaceOpenBeneathLongPath(t *testing.T) {
	_, dirfd := layout(t)

	cur := dirfd
	rel := ""
	for range 80 {
		comp := strings.Repeat("d", 50)
		require.NoError(t, unix.Mkdirat(cur, comp, 0o755))
		next, err := unix.Openat(cur, comp, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		require.NoError(t, err)
		if cur != dirfd {
			defer unix.Close(cur)
		}
		cur = next
		rel = filepath.Join(rel, comp)
	}
	defer unix.Close(cur)

	fd, err := unix.Openat(cur, "file.txt", unix.O_WRONLY|unix.O_CREAT, 0o644)
	require.NoError(t, err)
	_, _ = unix.Write(fd, []byte("deep"))
	_ = unix.Close(fd)

	require.Greater(t, len(rel), 4000)
	fd, err = userspaceOpenBeneath(dirfd, filepath.Join(rel, "file.txt"), unix.O_RDONLY, 0)
	require.NoError(t, err)
	assert.Equal(t, "deep", readFD(t, fd))
}

// Every failure closes the directories it opened on the way.
func TestUserspaceOpenBeneathDoesNotLeakDescriptors(t *testing.T) {
	base, dirfd := layout(t)
	require.NoError(t, os.Symlink("../secret.txt", filepath.Join(base, "a", "b", "out")))
	require.NoError(t, os.Symlink("loop", filepath.Join(base, "a", "loop")))

	before := openFDs(t)
	for range 200 {
		for _, p := range []string{
			"a/b/file.txt", "a/b/out", "a/loop", "a/b/missing", "a/b/file.txt/x", "a/../..", "../x",
		} {
			if fd, err := userspaceOpenBeneath(dirfd, p, unix.O_RDONLY, 0); err == nil {
				_ = unix.Close(fd)
			}
		}
	}
	assert.Equal(t, before, openFDs(t))
}

func TestForceUserspaceResolveIsHonored(t *testing.T) {
	if os.Getenv("UFS_TEST_USERSPACE_RESOLVE") != "1" {
		t.Skip("only checked when the suite runs on the userspace resolver")
	}
	assert.True(t, forceUserspaceResolve)

	fs, err := NewUnixFS(t.TempDir(), false)
	require.NoError(t, err)
	assert.True(t, fs.useOpenat2, "the resolver is used for every filesystem")
}

// A link out of the base is refused whichever way paths are resolved, with the
// error for what it is in the path: a bad resolution when something is opened
// through it, and "not a directory" when it is itself the directory that is
// opened, as it is never followed there. Both resolvers have to agree on this.
func TestLinkOutOfTheBaseInAPath(t *testing.T) {
	for _, userspace := range []bool{false, true} {
		forceUserspaceResolve = userspace
		defer func() { forceUserspaceResolve = false }()

		root := t.TempDir()
		outside := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(outside, "sub"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(outside, "sub", "secret.txt"), []byte("secret"), 0o644))

		fs, err := NewUnixFS(root, false)
		require.NoError(t, err)
		require.NoError(t, fs.Mkdir("d", 0o755))
		require.NoError(t, fs.Symlink(outside, "d/escape"))

		// The link is in the middle of the directory that holds the file.
		_, err = fs.Open("d/escape/sub/secret.txt")
		assert.ErrorIs(t, err, ErrBadPathResolution, "userspace=%v", userspace)

		// The link is the directory that holds the file.
		_, err = fs.Open("d/escape/secret.txt")
		assert.ErrorIs(t, err, ErrNotDirectory, "userspace=%v", userspace)

		// Nothing was created or changed outside of the base through the link.
		_, err = fs.Create("d/escape/sub/new.txt")
		assert.Error(t, err, "userspace=%v", userspace)
		_, err = os.Stat(filepath.Join(outside, "sub", "new.txt"))
		assert.ErrorIs(t, err, os.ErrNotExist, "userspace=%v", userspace)
	}
}
