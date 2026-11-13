package filesystem

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/internal/ufs"
)

type archiveInfoEntry struct {
	ufs.DirEntry
	info ufs.FileInfo
}

func (e archiveInfoEntry) Info() (ufs.FileInfo, error) { return e.info, nil }

type archiveReadErrorFile struct {
	ufs.File
	err error
}

func (f archiveReadErrorFile) Read([]byte) (int, error) { return 0, f.err }
func (archiveReadErrorFile) Close() error               { return nil }

func TestAddToArchiveHandlesSymlinkOpenAndReadErrors(t *testing.T) {
	root := t.TempDir()
	fs, err := New(root, 0, nil)
	require.NoError(t, err)
	archiveWithWriter := func() (*Archive, *bytes.Buffer) {
		out := &bytes.Buffer{}
		return &Archive{Filesystem: fs, w: NewTarProgress(tar.NewWriter(out), nil)}, out
	}
	oldReadlink, oldOpen, oldHeader := filesystemReadlinkat, filesystemOpenFileat, archiveFileInfoHeader
	t.Cleanup(func() {
		filesystemReadlinkat, filesystemOpenFileat, archiveFileInfoHeader = oldReadlink, oldOpen, oldHeader
	})

	t.Run("symlink readlink error is ignored", func(t *testing.T) {
		link := filepath.Join(root, "link")
		require.NoError(t, os.Symlink("target", link))
		info, err := os.Lstat(link)
		require.NoError(t, err)
		filesystemReadlinkat = func(*ufs.UnixFS, int, string) (string, error) {
			return "", errors.New("readlink failed")
		}
		archive, out := archiveWithWriter()
		require.NoError(t, archive.addToArchive(0, "link", "link", archiveInfoEntry{info: info}))
		require.Zero(t, out.Len())
	})

	filePath := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(filePath, []byte("contents"), 0o600))
	info, err := os.Stat(filePath)
	require.NoError(t, err)
	entry := archiveInfoEntry{info: info}
	t.Run("tar header error is returned", func(t *testing.T) {
		archiveFileInfoHeader = func(os.FileInfo, string) (*tar.Header, error) {
			return nil, errors.New("header failed")
		}
		archive, _ := archiveWithWriter()
		require.Error(t, archive.addToArchive(0, "file", "file", entry))
		archiveFileInfoHeader = oldHeader
	})
	for _, tc := range []struct {
		name    string
		openErr error
		readErr error
		wantErr bool
	}{
		{name: "missing file during open", openErr: os.ErrNotExist},
		{name: "other open error", openErr: errors.New("open failed"), wantErr: true},
		{name: "copy error", readErr: errors.New("read failed"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filesystemOpenFileat = func(*ufs.UnixFS, int, string, int, ufs.FileMode) (ufs.File, error) {
				if tc.readErr != nil {
					return archiveReadErrorFile{err: tc.readErr}, nil
				}
				return nil, tc.openErr
			}
			archive, _ := archiveWithWriter()
			err := archive.addToArchive(0, "file", "file", entry)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
