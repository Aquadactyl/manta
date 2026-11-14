package filesystem

import (
	"errors"
	fsys "io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type regularSizeDirEntry struct{}

func (regularSizeDirEntry) Name() string               { return "file" }
func (regularSizeDirEntry) IsDir() bool                { return false }
func (regularSizeDirEntry) Type() fsys.FileMode        { return 0 }
func (regularSizeDirEntry) Info() (os.FileInfo, error) { return nil, nil }

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
		{name: "removed while walking", walkErr: os.ErrNotExist},
		{name: "walk failure", walkErr: walkFailure, wantErr: walkFailure},
		{name: "removed before stat", walkFile: true, statErr: os.ErrNotExist},
		{name: "stat failure", walkFile: true, statErr: statFailure, wantErr: statFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			walkDirectoryForSize = func(_ fsys.FS, name string, fn fsys.WalkDirFunc) error {
				if tc.walkErr != nil {
					return fn(name, nil, tc.walkErr)
				}
				if tc.walkFile {
					return fn("file", regularSizeDirEntry{}, nil)
				}
				return nil
			}
			lstatForDirectorySize = func(*os.Root, string) (os.FileInfo, error) {
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
