package filesystem

import (
	"errors"
	"os"
	"testing"

	"github.com/pterodactyl/wings/internal/ufs"
	"github.com/stretchr/testify/require"
)

type testDirectoryEntry struct {
	info    ufs.FileInfo
	infoErr error
	mode    os.FileMode
	file    ufs.File
	openErr error
}

func (e testDirectoryEntry) Name() string                { return e.info.Name() }
func (e testDirectoryEntry) IsDir() bool                 { return e.mode.IsDir() }
func (e testDirectoryEntry) Type() os.FileMode           { return e.mode.Type() }
func (e testDirectoryEntry) Info() (ufs.FileInfo, error) { return e.info, e.infoErr }
func (e testDirectoryEntry) Open() (ufs.File, error)     { return e.file, e.openErr }

type failingMIMEFile struct {
	ufs.File
	err error
}

func (f failingMIMEFile) Read([]byte) (int, error) { return 0, f.err }
func (f failingMIMEFile) Close() error             { return nil }

func TestStatDirectoryEntryErrors(t *testing.T) {
	path := t.TempDir() + "/entry"
	require.NoError(t, os.WriteFile(path, []byte("contents"), 0o644))
	info, err := os.Stat(path)
	require.NoError(t, err)

	t.Run("info error", func(t *testing.T) {
		want := errors.New("info failed")
		_, err := statDirectoryEntry(testDirectoryEntry{info: info, infoErr: want, mode: info.Mode()})
		require.ErrorIs(t, err, want)
	})

	t.Run("open error", func(t *testing.T) {
		want := errors.New("open failed")
		_, err := statDirectoryEntry(testDirectoryEntry{info: info, mode: info.Mode(), openErr: want})
		require.ErrorIs(t, err, want)
	})

	t.Run("MIME read error retains the fallback type", func(t *testing.T) {
		entry := testDirectoryEntry{
			info: info, mode: info.Mode(), file: failingMIMEFile{err: errors.New("read failed")},
		}
		stat, err := statDirectoryEntry(entry)
		require.NoError(t, err)
		require.Equal(t, "application/octet-stream", stat.Mimetype)
	})
}

var _ ufs.File = failingMIMEFile{}
