package sftp

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/apex/log"
	pkgsftp "github.com/pkg/sftp"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
)

type dummyFileInfo struct {
	name string
}

func (d dummyFileInfo) Name() string       { return d.name }
func (d dummyFileInfo) Size() int64        { return 0 }
func (d dummyFileInfo) Mode() os.FileMode  { return 0644 }
func (d dummyFileInfo) ModTime() time.Time { return time.Time{} }
func (d dummyFileInfo) IsDir() bool        { return false }
func (d dummyFileInfo) Sys() any           { return nil }

func TestListerAt(t *testing.T) {
	files := ListerAt{
		dummyFileInfo{name: "f1"},
		dummyFileInfo{name: "f2"},
		dummyFileInfo{name: "f3"},
	}

	// Offset out of range
	buf := make([]os.FileInfo, 2)
	n, err := files.ListAt(buf, 5)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)

	// Partial copy at end of slice
	n, err = files.ListAt(buf, 2)
	assert.Equal(t, 1, n)
	assert.Equal(t, io.EOF, err)
	assert.Equal(t, "f3", buf[0].Name())

	// Exact copy matching buffer size
	n, err = files.ListAt(buf, 0)
	assert.Equal(t, 2, n)
	assert.NoError(t, err)
	assert.Equal(t, "f1", buf[0].Name())
	assert.Equal(t, "f2", buf[1].Name())
}

func TestFxErr(t *testing.T) {
	assert.Equal(t, "Quota Exceeded", ErrSSHQuotaExceeded.Error())
	assert.Equal(t, "Failure", fxErr(99).Error())

	// As tests
	var target int
	assert.False(t, ErrSSHQuotaExceeded.As(target))
	assert.False(t, ErrSSHQuotaExceeded.As(&target))
	assert.False(t, ErrSSHQuotaExceeded.As(nil))
}

func TestPrivateKeyGeneration(t *testing.T) {
	tmp := t.TempDir()
	server := &SFTPServer{
		BasePath: tmp,
	}

	assert.Equal(t, filepath.Join(tmp, ".sftp/id_ed25519"), server.PrivateKeyPath())

	require.NoError(t, server.generateED25519PrivateKey())
	assert.FileExists(t, server.PrivateKeyPath())

	data, err := os.ReadFile(server.PrivateKeyPath())
	require.NoError(t, err)
	assert.Contains(t, string(data), "-----BEGIN PRIVATE KEY-----")
}

type stringAddr string

func (a stringAddr) Network() string { return "test" }
func (a stringAddr) String() string  { return string(a) }

func TestSftpNewAndConnectionKey(t *testing.T) {
	setSessionTestConfig(t)
	m := server.NewEmptyManager(nil)
	srv := New(m)
	assert.NotNil(t, srv)
	assert.Equal(t, m, srv.manager)

	// IPv6 connectionKey
	k6 := connectionKey(&net.TCPAddr{IP: net.ParseIP("2001:db8::1234"), Port: 2022})
	assert.Equal(t, "2001:db8::/64", k6)

	// Non-hostport stringAddr
	kRaw := connectionKey(stringAddr("plain-string"))
	assert.Equal(t, "plain-string", kRaw)
}

func TestEventHandlerLog(t *testing.T) {
	tmp := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			RootDirectory: tmp,
		},
	})
	require.NoError(t, database.Initialize())

	eh := &eventHandler{
		ip:     "127.0.0.1",
		user:   "user-uuid",
		server: "server-uuid",
	}

	// Single entity
	err := eh.Log(models.Event("sftp:file.write"), FileAction{Entity: "/foo.txt"})
	require.NoError(t, err)

	// Rename/Target
	err = eh.Log(models.Event("sftp:file.rename"), FileAction{Entity: "/foo.txt", Target: "/bar.txt"})
	require.NoError(t, err)

	// MustLog
	assert.NotPanics(t, func() {
		eh.MustLog(models.Event("sftp:file.delete"), FileAction{Entity: "/bar.txt"})
	})
}

func TestHandlerOperations(t *testing.T) {
	setSessionTestConfig(t)
	dir := t.TempDir()
	fsys, err := filesystem.New(dir, 0, nil)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("hello"), 0644))

	srv, err := server.New(nil)
	require.NoError(t, err)

	events := &eventHandler{ip: "127.0.0.1", user: "u1", server: "s1"}
	h := &Handler{
		permissions: []string{"*"},
		server:      srv,
		fs:          fsys,
		events:      events,
		ro:          false,
		logger:      log.WithFields(log.Fields{}),
	}

	// 1. Filewrite new file
	w, err := h.Filewrite(&pkgsftp.Request{Filepath: "new.txt"})
	require.NoError(t, err)
	n, err := w.WriteAt([]byte("written content"), 0)
	assert.NoError(t, err)
	assert.Equal(t, 15, n)
	if closer, ok := w.(io.Closer); ok {
		_ = closer.Close()
	}

	// 2. Filewrite existing file
	w, err = h.Filewrite(&pkgsftp.Request{Filepath: "existing.txt"})
	require.NoError(t, err)
	if closer, ok := w.(io.Closer); ok {
		_ = closer.Close()
	}

	// 3. Fileread
	r, err := h.Fileread(&pkgsftp.Request{Filepath: "new.txt"})
	require.NoError(t, err)
	readBuf := make([]byte, 15)
	n, err = r.ReadAt(readBuf, 0)
	assert.NoError(t, err)
	assert.Equal(t, "written content", string(readBuf[:n]))

	// 4. Filelist - List
	lister, err := h.Filelist(&pkgsftp.Request{Method: "List", Filepath: "/"})
	require.NoError(t, err)
	statBuf := make([]os.FileInfo, 5)
	count, _ := lister.ListAt(statBuf, 0)
	assert.True(t, count >= 2)

	// 5. Filelist - Stat
	lister, err = h.Filelist(&pkgsftp.Request{Method: "Stat", Filepath: "new.txt"})
	require.NoError(t, err)
	statBuf = make([]os.FileInfo, 1)
	count, _ = lister.ListAt(statBuf, 0)
	assert.Equal(t, 1, count)
	assert.Equal(t, "new.txt", statBuf[0].Name())

	// 6. Filecmd - Mkdir
	err = h.Filecmd(&pkgsftp.Request{Method: "Mkdir", Filepath: "/folder"})
	assert.True(t, err == nil || err == pkgsftp.ErrSSHFxOk)

	// 7. Filecmd - Rename
	err = h.Filecmd(&pkgsftp.Request{Method: "Rename", Filepath: "new.txt", Target: "renamed.txt"})
	assert.True(t, err == nil || err == pkgsftp.ErrSSHFxOk)

	// 8. Filecmd - Setstat
	err = h.Filecmd(&pkgsftp.Request{Method: "Setstat", Filepath: "renamed.txt"})
	assert.True(t, err == nil || err == pkgsftp.ErrSSHFxOk)

	// 9. Filecmd - Symlink
	err = h.Filecmd(&pkgsftp.Request{Method: "Symlink", Filepath: "renamed.txt", Target: "sym.txt"})
	assert.True(t, err == nil || err == pkgsftp.ErrSSHFxOk)

	// 10. Filecmd - Remove
	err = h.Filecmd(&pkgsftp.Request{Method: "Remove", Filepath: "sym.txt"})
	assert.True(t, err == nil || err == pkgsftp.ErrSSHFxOk)

	// 11. Filecmd - Rmdir
	err = h.Filecmd(&pkgsftp.Request{Method: "Rmdir", Filepath: "/folder"})
	assert.True(t, err == nil || err == pkgsftp.ErrSSHFxOk)

	// 12. Filecmd - Unsupported
	err = h.Filecmd(&pkgsftp.Request{Method: "Unknown"})
	assert.Equal(t, pkgsftp.ErrSSHFxOpUnsupported, err)

	// Readonly checks
	hRO := &Handler{ro: true}
	_, err = hRO.Filewrite(&pkgsftp.Request{})
	assert.Equal(t, pkgsftp.ErrSSHFxOpUnsupported, err)
	err = hRO.Filecmd(&pkgsftp.Request{})
	assert.Equal(t, pkgsftp.ErrSSHFxOpUnsupported, err)

	// Permission denied checks
	hNoPerm := &Handler{permissions: []string{}, server: srv, fs: fsys, logger: log.WithFields(log.Fields{})}
	_, err = hNoPerm.Fileread(&pkgsftp.Request{Filepath: "existing.txt"})
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, err)
	_, err = hNoPerm.Filewrite(&pkgsftp.Request{Filepath: "existing.txt"})
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, err)
	_, err = hNoPerm.Filelist(&pkgsftp.Request{Method: "List", Filepath: "/"})
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, err)
}


