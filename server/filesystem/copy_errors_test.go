package filesystem

import (
	"errors"
	"strings"
	"testing"

	"github.com/pterodactyl/wings/internal/ufs"
	"github.com/stretchr/testify/require"
)

type copyStatErrorFile struct {
	ufs.File
	err error
}

func (f copyStatErrorFile) Stat() (ufs.FileInfo, error) { return nil, f.err }

func TestCopyReturnsSourceStatError(t *testing.T) {
	fs, err := New(t.TempDir(), 0, nil)
	require.NoError(t, err)
	fs.SetTest(true)
	require.NoError(t, fs.Writefile("source.txt", strings.NewReader("data")))
	t.Cleanup(func() { _ = fs.unixFS.Close() })

	want := errors.New("source stat failed")
	previous := filesystemOpenFileat
	filesystemOpenFileat = func(unixFS *ufs.UnixFS, dirfd int, name string, flag int, mode ufs.FileMode) (ufs.File, error) {
		file, err := unixFS.OpenFileat(dirfd, name, flag, mode)
		if err != nil {
			return nil, err
		}
		return copyStatErrorFile{File: file, err: want}, nil
	}
	t.Cleanup(func() { filesystemOpenFileat = previous })

	require.ErrorIs(t, fs.Copy("source.txt"), want)
}
