package filesystem

import (
	"errors"
	"testing"

	"github.com/pterodactyl/wings/internal/ufs"
	"github.com/stretchr/testify/require"
)

type regularSizeDirEntry struct{}

func (regularSizeDirEntry) Name() string                { return "file" }
func (regularSizeDirEntry) IsDir() bool                 { return false }
func (regularSizeDirEntry) Type() ufs.FileMode          { return 0 }
func (regularSizeDirEntry) Info() (ufs.FileInfo, error) { return nil, nil }

func TestDirectorySizeHandlesEntriesRemovedDuringWalk(t *testing.T) {
	oldWalk, oldLstat := walkDirectoryForSize, lstatForDirectorySize
	t.Cleanup(func() { walkDirectoryForSize, lstatForDirectorySize = oldWalk, oldLstat })
	fs, err := New(t.TempDir(), 0, nil)
	require.NoError(t, err)

	walkFailure := errors.New("walk failed")
	statFailure := errors.New("stat failed")
	for _, tc := range []struct {
		name     string
		walkErr  error
		statErr  error
		walkFile bool
		wantErr  error
	}{
		{name: "removed while walking", walkErr: ufs.ErrNotExist},
		{name: "walk failure", walkErr: walkFailure, wantErr: walkFailure},
		{name: "removed before stat", walkFile: true, statErr: ufs.ErrNotExist},
		{name: "stat failure", walkFile: true, statErr: statFailure, wantErr: statFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			walkDirectoryForSize = func(_ *ufs.Quota, dirfd int, name string, fn ufs.WalkDiratFunc) error {
				if tc.walkErr != nil {
					return fn(dirfd, name, ".", regularSizeDirEntry{}, tc.walkErr)
				}
				if tc.walkFile {
					return fn(dirfd, "file", "file", regularSizeDirEntry{}, nil)
				}
				return nil
			}
			lstatForDirectorySize = func(*ufs.Quota, int, string) (ufs.FileInfo, error) {
				return nil, tc.statErr
			}
			size, err := fs.DirectorySize("/")
			require.Zero(t, size)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}
