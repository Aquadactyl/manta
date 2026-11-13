package ufs

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestOpenatPreservesRetryableErrorsAndWrapsOthers(t *testing.T) {
	oldOpenat := ufsOpenat
	t.Cleanup(func() { ufsOpenat = oldOpenat })
	fs := &UnixFS{}

	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{name: "interrupted", err: unix.EINTR, want: unix.EINTR},
		{name: "try again", err: unix.EAGAIN, want: unix.EAGAIN},
		{name: "other syscall error", err: unix.EACCES, want: unix.EACCES},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotFlags int
			ufsOpenat = func(_ int, _ string, flags int, _ uint32) (int, error) {
				gotFlags = flags
				return 42, tc.err
			}
			fd, err := fs._openat(AT_EMPTY_PATH, "file", O_RDONLY, 0)
			require.Equal(t, 42, fd)
			require.Error(t, err)
			if tc.err == unix.EINTR || tc.err == unix.EAGAIN {
				require.ErrorIs(t, err, tc.want)
			} else {
				var pathErr *PathError
				require.ErrorAs(t, err, &pathErr)
				require.Equal(t, "openat", pathErr.Op)
				require.Equal(t, "file", pathErr.Path)
				require.ErrorIs(t, err, tc.want)
			}
			require.NotZero(t, gotFlags&O_CLOEXEC)
		})
	}
}

func TestOpenatKeepsCloexecWhenAlreadySet(t *testing.T) {
	oldOpenat := ufsOpenat
	t.Cleanup(func() { ufsOpenat = oldOpenat })
	var gotFlags int
	ufsOpenat = func(_ int, _ string, flags int, _ uint32) (int, error) {
		gotFlags = flags
		return 7, nil
	}

	fd, err := (&UnixFS{})._openat(AT_EMPTY_PATH, "file", O_RDONLY|O_CLOEXEC, 0)
	require.NoError(t, err)
	require.Equal(t, 7, fd)
	require.NotZero(t, gotFlags&O_CLOEXEC)
}
