package filesystem

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type copyStatErrorFile struct {
	filesystemFile
	err error
}

func (f copyStatErrorFile) Stat() (os.FileInfo, error) { return nil, f.err }

func TestCopyReturnsSourceStatError(t *testing.T) {
	fs, err := New(t.TempDir(), 0, nil)
	require.NoError(t, err)
	fs.SetTest(true)
	require.NoError(t, fs.Writefile("source.txt", strings.NewReader("data")))
	t.Cleanup(func() { _ = fs.Close() })

	want := errors.New("source stat failed")
	previous := filesystemOpen
	filesystemOpen = func(fsys *Filesystem, name string) (filesystemFile, error) {
		file, err := previous(fsys, name)
		if err != nil {
			return nil, err
		}
		return copyStatErrorFile{filesystemFile: file, err: want}, nil
	}
	t.Cleanup(func() { filesystemOpen = previous })

	require.ErrorIs(t, fs.Copy("source.txt"), want)
}
