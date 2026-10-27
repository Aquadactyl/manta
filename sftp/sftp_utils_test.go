package sftp

import (
	"context"
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
	"golang.org/x/crypto/ssh"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/remote"
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
	func() {
		defer func() { _ = recover() }()
		_ = database.Initialize()
	}()

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
	func() {
		defer func() { _ = recover() }()
		_ = database.Initialize()
	}()
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

	// NoSuchFile checks for Filecmd
	assert.Equal(t, pkgsftp.ErrSSHFxNoSuchFile, h.Filecmd(&pkgsftp.Request{Method: "Setstat", Filepath: "missing.txt"}))
	assert.Equal(t, pkgsftp.ErrSSHFxNoSuchFile, h.Filecmd(&pkgsftp.Request{Method: "Rename", Filepath: "missing.txt", Target: "target.txt"}))
	assert.Equal(t, pkgsftp.ErrSSHFxOk, h.Filecmd(&pkgsftp.Request{Method: "Remove", Filepath: "missing.txt"}))

	// Filelist checks
	// Stat on missing file
	_, err = h.Filelist(&pkgsftp.Request{Method: "Stat", Filepath: "missing.txt"})
	assert.Equal(t, pkgsftp.ErrSSHFxNoSuchFile, err)

	// List on missing dir
	_, err = h.Filelist(&pkgsftp.Request{Method: "List", Filepath: "missing_dir"})
	assert.Equal(t, pkgsftp.ErrSSHFxFailure, err)

	// Unsupported method on Filelist
	_, err = h.Filelist(&pkgsftp.Request{Method: "Unknown", Filepath: "/"})
	assert.Equal(t, pkgsftp.ErrSSHFxOpUnsupported, err)

	// Suspended / Protected state checks in can()
	srv.Config().Suspended = true
	assert.False(t, h.can(PermissionFileRead))
	srv.Config().Suspended = false

	srv.SetInstalling(true)
	assert.False(t, h.can(PermissionFileRead))
	srv.SetInstalling(false)
}

func TestSFTPServerPrivateKeyAndUtils(t *testing.T) {
	tmp := t.TempDir()
	s := &SFTPServer{BasePath: tmp}

	// 1. PrivateKeyPath
	pkPath := s.PrivateKeyPath()
	assert.Equal(t, filepath.Join(tmp, ".sftp/id_ed25519"), pkPath)

	// 2. generateED25519PrivateKey
	err := s.generateED25519PrivateKey()
	require.NoError(t, err)
	assert.FileExists(t, pkPath)

	fileParent := filepath.Join(tmp, "file_parent")
	require.NoError(t, os.WriteFile(fileParent, []byte("hello"), 0644))
	sBad := &SFTPServer{BasePath: fileParent}
	assert.Error(t, sBad.generateED25519PrivateKey())

	// 3. connectionKey
	tcpAddr, err := net.ResolveTCPAddr("tcp", "1.2.3.4:12345")
	require.NoError(t, err)
	assert.Equal(t, "1.2.3.4", connectionKey(tcpAddr))

	udpAddr, err := net.ResolveUDPAddr("udp", "5.6.7.8:54321")
	require.NoError(t, err)
	assert.Equal(t, "5.6.7.8", connectionKey(udpAddr))

	// 4. configuredLimits
	sftpCfg := config.SftpConfiguration{}
	limits := configuredLimits(sftpCfg)
	assert.True(t, limits.maxPending > 0)
	assert.True(t, limits.maxPendingPerIP > 0)

	// 5. droppedLog
	dl := &droppedLog{}
	dl.log("testing dropped log", "test-key")
}

type mockConnMetadata struct {
	user       string
	remoteAddr net.Addr
}

func (m *mockConnMetadata) User() string          { return m.user }
func (m *mockConnMetadata) SessionID() []byte     { return []byte("session-id") }
func (m *mockConnMetadata) ClientVersion() []byte { return []byte("SSH-2.0-Test") }
func (m *mockConnMetadata) ServerVersion() []byte { return []byte("SSH-2.0-Server") }
func (m *mockConnMetadata) RemoteAddr() net.Addr  { return m.remoteAddr }
func (m *mockConnMetadata) LocalAddr() net.Addr   { return m.remoteAddr }

type mockSftpClient struct {
	remote.Client
	resp remote.SftpAuthResponse
	err  error
}

func (m *mockSftpClient) ValidateSftpCredentials(_ context.Context, _ remote.SftpAuthRequest) (remote.SftpAuthResponse, error) {
	return m.resp, m.err
}

func TestMakeCredentialsRequest(t *testing.T) {
	cli := &mockSftpClient{}
	mgr := server.NewEmptyManager(cli)
	srv := &SFTPServer{manager: mgr}

	addr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:2222")

	// 1. Invalid username
	conn := &mockConnMetadata{user: "invalid@@username", remoteAddr: addr}
	_, err := srv.makeCredentialsRequest(conn, remote.SftpAuthPassword, "password")
	assert.Error(t, err)
	assert.IsType(t, &remote.SftpInvalidCredentialsError{}, err)

	// 2. Client returns SftpInvalidCredentialsError
	conn = &mockConnMetadata{user: "validuser.12345678", remoteAddr: addr}
	cli.err = &remote.SftpInvalidCredentialsError{}
	_, err = srv.makeCredentialsRequest(conn, remote.SftpAuthPassword, "password")
	assert.ErrorIs(t, err, cli.err)

	// 3. Client returns generic error
	cli.err = assert.AnError
	_, err = srv.makeCredentialsRequest(conn, remote.SftpAuthPassword, "password")
	assert.ErrorIs(t, err, assert.AnError)

	// 4. Client returns successful response
	cli.err = nil
	cli.resp = remote.SftpAuthResponse{
		Server:      "srv-uuid-1234",
		User:        "user-uuid-5678",
		Permissions: []string{"read", "write"},
	}
	perms, err := srv.makeCredentialsRequest(conn, remote.SftpAuthPassword, "password")
	require.NoError(t, err)
	require.NotNil(t, perms)
	assert.Equal(t, "srv-uuid-1234", perms.Extensions["uuid"])
	assert.Equal(t, "user-uuid-5678", perms.Extensions["user"])
	assert.Equal(t, "read,write", perms.Extensions["permissions"])
}

func TestEventMustLog(t *testing.T) {
	func() {
		defer func() { _ = recover() }()
		_ = database.Initialize()
	}()

	eh := &eventHandler{
		server: "srv-uuid",
		user:   "user-uuid",
		ip:     "127.0.0.1",
	}

	eh.MustLog("test_event", FileAction{Entity: "/file.txt"})
	eh.MustLog("test_event", FileAction{Entity: "/from.txt", Target: "/to.txt"})
}

func TestSFTPAdditionalCoverage(t *testing.T) {
	// 1. sessionKey with permissions
	sconn := &ssh.ServerConn{
		Permissions: &ssh.Permissions{
			Extensions: map[string]string{
				"user": "alice",
				"uuid": "server-1",
			},
		},
	}
	assert.Equal(t, "alice:server-1", sessionKey(sconn))

	// 2. connectionKey with non-IP hostname
	assert.Equal(t, "example.com", connectionKey(stringAddr("example.com:2022")))

	// 3. SFTPServer.Run error when listen address invalid
	tmp := t.TempDir()
	srv := &SFTPServer{
		BasePath: tmp,
		Listen:   "256.256.256.256:2022",
	}
	err := srv.Run()
	assert.Error(t, err)

	// SFTPServer.Run with invalid private key content
	tmp2 := t.TempDir()
	srv2 := &SFTPServer{BasePath: tmp2}
	require.NoError(t, os.MkdirAll(filepath.Dir(srv2.PrivateKeyPath()), 0755))
	require.NoError(t, os.WriteFile(srv2.PrivateKeyPath(), []byte("invalid-key-data"), 0600))
	assert.Error(t, srv2.Run())

	// 4. AcceptInbound error on closed connection
	pr, pw := net.Pipe()
	_ = pr.Close()
	srvAccept := &SFTPServer{}
	assert.Error(t, srvAccept.AcceptInbound(pw, &ssh.ServerConfig{}))
	_ = pw.Close()

	// 5. MustLog error branch
	ehBad := &eventHandler{}
	ehBad.MustLog("test_err_event", FileAction{})
}

func TestFilecmdBranches(t *testing.T) {
	tmp := t.TempDir()
	fsys, err := filesystem.New(tmp, 0, nil)
	require.NoError(t, err)

	srv, err := server.New(nil)
	require.NoError(t, err)

	// Handler without permissions
	hNoPerm := &Handler{
		server:      srv,
		fs:          fsys,
		logger:      log.WithField("test", "test"),
		permissions: []string{},
	}

	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, hNoPerm.Filecmd(&pkgsftp.Request{Method: "Setstat", Filepath: "/f"}))
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, hNoPerm.Filecmd(&pkgsftp.Request{Method: "Rename", Filepath: "/f", Target: "/t"}))
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, hNoPerm.Filecmd(&pkgsftp.Request{Method: "Rmdir", Filepath: "/d"}))
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, hNoPerm.Filecmd(&pkgsftp.Request{Method: "Remove", Filepath: "/f"}))
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, hNoPerm.Filecmd(&pkgsftp.Request{Method: "Mkdir", Filepath: "/d"}))

	// Handler with permissions but non-existent files
	hFull := &Handler{
		server:      srv,
		fs:          fsys,
		logger:      log.WithField("test", "test"),
		permissions: []string{PermissionFileUpdate, PermissionFileDelete, PermissionFileCreate},
	}

	assert.Equal(t, pkgsftp.ErrSSHFxNoSuchFile, hFull.Filecmd(&pkgsftp.Request{Method: "Rename", Filepath: "/nonexistent", Target: "/newname"}))
	assert.Equal(t, pkgsftp.ErrSSHFxOpUnsupported, hFull.Filecmd(&pkgsftp.Request{Method: "UnsupportedMethod"}))
}





