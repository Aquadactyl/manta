//go:build linux

package ufs

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestOpenat2SetsRequiredFlagsAndPreservesRetryableErrors(t *testing.T) {
	oldOpenat2 := ufsOpenat2
	t.Cleanup(func() { ufsOpenat2 = oldOpenat2 })

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "interrupted", err: unix.EINTR},
		{name: "try again", err: unix.EAGAIN},
		{name: "other syscall error", err: unix.EACCES},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotHow unix.OpenHow
			ufsOpenat2 = func(_ int, _ string, how *unix.OpenHow) (int, error) {
				gotHow = *how
				return 42, tc.err
			}
			fd, err := (&UnixFS{})._openat2(AT_EMPTY_PATH, "file", O_RDONLY, 0)
			require.Equal(t, 42, fd)
			require.NotZero(t, gotHow.Flags&O_CLOEXEC)
			if tc.err == nil {
				require.NoError(t, err)
			} else if tc.err == unix.EINTR || tc.err == unix.EAGAIN {
				require.ErrorIs(t, err, tc.err)
			} else {
				var pathErr *PathError
				require.ErrorAs(t, err, &pathErr)
				require.Equal(t, "openat2", pathErr.Op)
				require.Equal(t, "file", pathErr.Path)
				require.ErrorIs(t, err, tc.err)
			}
		})
	}
}
