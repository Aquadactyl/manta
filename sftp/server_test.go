package sftp

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

func startTestServer(t *testing.T, limits connectionLimits) string {
	t.Helper()
	return startTestServerWithAuth(t, limits, func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
		return nil, errors.New("denied")
	})
}

func startTestServerWithAuth(t *testing.T, limits connectionLimits, auth func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error)) string {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	conf := &ssh.ServerConfig{PasswordCallback: auth}
	conf.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	srv := &SFTPServer{limits: limits}
	go func() {
		_ = srv.serve(listener, conf)
	}()
	return listener.Addr().String()
}

// dial opens a connection from the given loopback address and reports whether
// the server accepted it, which it signals by sending its version banner.
func dial(t *testing.T, addr string, from string) (net.Conn, bool) {
	t.Helper()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(from)}}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(time.Second * 2))
	line, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.SetReadDeadline(time.Time{})
	return conn, err == nil && strings.HasPrefix(line, "SSH-")
}

func TestUnauthenticatedConnectionsAreClosedAfterTimeout(t *testing.T) {
	addr := startTestServer(t, connectionLimits{handshakeTimeout: time.Millisecond * 250, maxPending: 128, maxPendingPerIP: 16})

	conn, ok := dial(t, addr, "127.0.0.1")
	if !ok {
		t.Fatal("expected connection to be accepted")
	}

	// Never send anything; the server must close the connection on its own.
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	_, err := conn.Read(make([]byte, 1))
	if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected the server to close an idle unauthenticated connection, got %v after %s", err, time.Since(start))
	}
}

func TestUnauthenticatedConnectionsAreLimited(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Darwin CI does not permit binding TCP clients to additional loopback addresses")
	}
	addr := startTestServer(t, connectionLimits{handshakeTimeout: time.Minute, maxPending: 3, maxPendingPerIP: 2})

	// Two connections from one address are accepted, the third is not.
	first, ok := dial(t, addr, "127.0.0.1")
	if !ok {
		t.Fatal("expected first connection to be accepted")
	}
	if _, ok := dial(t, addr, "127.0.0.1"); !ok {
		t.Fatal("expected second connection to be accepted")
	}
	if _, ok := dial(t, addr, "127.0.0.1"); ok {
		t.Fatal("expected third connection from the same address to be dropped")
	}

	// Another address has its own limit, until the total limit is reached.
	if _, ok := dial(t, addr, "127.0.0.2"); !ok {
		t.Fatal("expected connection from another address to be accepted")
	}
	if _, ok := dial(t, addr, "127.0.0.3"); ok {
		t.Fatal("expected connection to be dropped once the total limit is reached")
	}

	// Closing a pending connection frees up its slot.
	_ = first.Close()
	deadline := time.Now().Add(time.Second * 5)
	for {
		if _, ok := dial(t, addr, "127.0.0.1"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected slot to be released when a pending connection closes")
		}
		time.Sleep(time.Millisecond * 50)
	}
}

func TestConnectionCounterLimitsAndRelease(t *testing.T) {
	var cc connectionCounter
	if !cc.acquire("user-a", 3, 2) {
		t.Fatal("expected first connection for a key to be accepted")
	}
	if !cc.acquire("user-a", 3, 2) {
		t.Fatal("expected two connections for one key to be accepted")
	}
	if cc.acquire("user-a", 3, 2) {
		t.Fatal("expected per-key limit to reject another connection")
	}
	if !cc.acquire("user-b", 3, 2) {
		t.Fatal("expected a different key to be accepted")
	}
	if cc.acquire("user-c", 3, 2) {
		t.Fatal("expected total limit to reject another connection")
	}
	cc.release("user-a")
	if !cc.acquire("user-a", 3, 2) {
		t.Fatal("expected release to free the per-key and total slots")
	}
}

type oneConnectionListener struct {
	conn net.Conn
	used bool
}

func (l *oneConnectionListener) Accept() (net.Conn, error) {
	if !l.used {
		l.used = true
		return l.conn, nil
	}
	return nil, net.ErrClosed
}
func (*oneConnectionListener) Close() error   { return nil }
func (*oneConnectionListener) Addr() net.Addr { return testPipeAddr("pipe") }

type testPipeAddr string

func (a testPipeAddr) Network() string { return string(a) }
func (a testPipeAddr) String() string  { return string(a) }

func TestServeDropsConnectionAtPendingLimit(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	listener := &oneConnectionListener{conn: serverConn}
	srv := &SFTPServer{limits: connectionLimits{maxPending: 1, maxPendingPerIP: 1}}
	require.True(t, srv.pending.acquire("pipe", 1, 1))

	err := srv.serve(listener, &ssh.ServerConfig{})
	require.ErrorIs(t, err, net.ErrClosed)
	_, err = clientConn.Read(make([]byte, 1))
	require.Error(t, err)
}

// Every address in an IPv6 /64 network belongs to the same user, so they share a
// limit.
func TestConnectionKey(t *testing.T) {
	tests := map[string]string{
		"127.0.0.1:2022":                 "127.0.0.1",
		"[::ffff:10.0.0.1]:2022":         "10.0.0.1",
		"[2001:db8::1]:2022":             "2001:db8::/64",
		"[2001:db8::ffff:ffff]:2022":     "2001:db8::/64",
		"[2001:db8:0:1::1]:2022":         "2001:db8:0:1::/64",
		"[fe80::1%eth0]:2022":            "fe80::/64",
		"[2001:db8::abcd:1234:5678]:443": "2001:db8::/64",
	}
	for addr, want := range tests {
		a, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := connectionKey(a); got != want {
			t.Errorf("connectionKey(%s) = %s, expected %s", addr, got, want)
		}
	}
}

// Authenticated connections are limited per user, so that a single user cannot
// use up every connection.
func TestAuthenticatedConnectionsAreLimitedPerUser(t *testing.T) {
	limits := defaultLimits
	limits.maxPerUser = 2
	addr := startTestServerWithAuth(t, limits, func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
		return &ssh.Permissions{}, nil
	})
	connect := func(user string) (*ssh.Client, error) {
		return ssh.Dial("tcp", addr, &ssh.ClientConfig{
			User:            user,
			Auth:            []ssh.AuthMethod{ssh.Password("password")},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         time.Second * 5,
		})
	}
	// closedWithin reports whether the server closes the connection in time.
	closedWithin := func(c *ssh.Client, d time.Duration) bool {
		done := make(chan struct{})
		go func() {
			_ = c.Wait()
			close(done)
		}()
		select {
		case <-done:
			return true
		case <-time.After(d):
			return false
		}
	}

	var clients []*ssh.Client
	for i := 0; i < 2; i++ {
		c, err := connect("user.12345678")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		clients = append(clients, c)
	}
	if c, err := connect("user.12345678"); err == nil {
		t.Cleanup(func() { _ = c.Close() })
		if !closedWithin(c, time.Second*3) {
			t.Fatal("expected a third connection for the same user to be closed")
		}
	}
	if closedWithin(clients[0], time.Millisecond*200) {
		t.Fatal("expected the connections within the limit to stay open")
	}

	// Another user has their own limit.
	other, err := connect("other.12345678")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if closedWithin(other, time.Millisecond*500) {
		t.Fatal("expected a connection for another user to stay open")
	}
}

func TestSFTPServerRunAndHandleBranches(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := server.NewEmptyManager(nil)

	// 1. Run when PrivateKeyPath parent is a file -> os.Stat returns ENOTDIR (err != nil && !IsNotExist)
	s1 := New(mgr)
	s1.BasePath = blocker
	if err := s1.Run(); err == nil {
		t.Fatal("expected Run to fail when BasePath is a file")
	}

	// 2. Run when PrivateKeyPath does not exist and generateED25519PrivateKey fails because .sftp is a file
	s2Dir := filepath.Join(tmp, "s2")
	if err := os.MkdirAll(s2Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s2Dir, ".sftp"), []byte("not-a-dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	s2 := New(mgr)
	s2.BasePath = s2Dir
	if err := s2.Run(); err == nil {
		t.Fatal("expected Run to fail when .sftp is a file")
	}

	// 3. Run when PrivateKeyPath is a directory -> os.Stat succeeds, os.ReadFile fails;
	// also generateED25519PrivateKey fails on OpenFile when PrivateKeyPath is a directory
	s3Dir := filepath.Join(tmp, "s3")
	s3 := New(mgr)
	s3.BasePath = s3Dir
	if err := os.MkdirAll(s3.PrivateKeyPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s3.generateED25519PrivateKey(); err == nil {
		t.Fatal("expected generateED25519PrivateKey to fail when PrivateKeyPath is a directory")
	}
	if err := s3.Run(); err == nil {
		t.Fatal("expected Run to fail when PrivateKeyPath is a directory")
	}

	// 4. Run when PrivateKeyPath exists via generateED25519PrivateKey, but Listen address is invalid
	s4Dir := filepath.Join(tmp, "s4")
	s4 := New(mgr)
	s4.BasePath = s4Dir
	s4.Listen = "invalid:listen:addr:99999"
	if err := s4.Run(); err == nil {
		t.Fatal("expected Run to fail on invalid Listen address")
	}

	// 4b. Run when PrivateKeyPath contains invalid PEM bytes -> ssh.ParsePrivateKey fails
	if err := os.WriteFile(s4.PrivateKeyPath(), []byte("not-a-private-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s4.Run(); err == nil {
		t.Fatal("expected Run to fail on invalid private key bytes")
	}

	// 5. NewHandler and Handle when user extension is missing
	scNoUser := &ssh.ServerConn{
		Permissions: &ssh.Permissions{
			Extensions: map[string]string{},
		},
	}
	if _, err := NewHandler(scNoUser, nil); err == nil {
		t.Fatal("expected NewHandler to fail without user extension")
	}
	if err := s4.Handle(scNoUser, nil, nil); err == nil {
		t.Fatal("expected Handle to fail without user extension")
	}

	// 6. serve retry backoff on temporary Accept errors
	fl := &flakyListener{fails: 2}
	if err := s4.serve(fl, &ssh.ServerConfig{}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("expected net.ErrClosed from flaky listener, got %v", err)
	}

	// 7. AcceptInbound handshake failure and success
	c1, c2 := net.Pipe()
	_ = c1.Close()
	_ = c2.Close()
	if err := s4.AcceptInbound(c1, &ssh.ServerConfig{}); err == nil {
		t.Fatal("expected AcceptInbound to fail on closed pipe")
	}

	// 8. fxErr.As edge cases
	if ErrSSHQuotaExceeded.As("not-a-pointer") {
		t.Fatal("expected As to return false for non-pointer")
	}
	var nilPtr *uint32
	if ErrSSHQuotaExceeded.As(nilPtr) {
		t.Fatal("expected As to return false for nil pointer")
	}
	var wrongType uint32
	if ErrSSHQuotaExceeded.As(&wrongType) {
		t.Fatal("expected As to return false for non-sftp.fxerr uint32")
	}

	fxerrType := reflect.TypeOf((&pkgsftp.StatusError{Code: 1}).FxCode())
	target := reflect.New(fxerrType).Interface()
	if !ErrSSHQuotaExceeded.As(target) {
		t.Fatal("expected As to return true for fxerr target")
	}
}

func TestSFTPServerRunPropagatesHostKeyGenerationError(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	oldGenerate := generateED25519Key
	want := errors.New("entropy unavailable")
	generateED25519Key = func(io.Reader) (ed25519.PublicKey, ed25519.PrivateKey, error) {
		return nil, nil, want
	}
	t.Cleanup(func() { generateED25519Key = oldGenerate })
	srv := New(server.NewEmptyManager(nil))
	srv.BasePath = t.TempDir()
	require.ErrorIs(t, srv.Run(), want)
}

func TestSFTPServerAcceptBackoffCapsAtOneSecond(t *testing.T) {
	oldSleep := sleepSFTPBackoff
	t.Cleanup(func() { sleepSFTPBackoff = oldSleep })
	var longest time.Duration
	sleepSFTPBackoff = func(delay time.Duration) {
		if delay > longest {
			longest = delay
		}
	}
	srv := &SFTPServer{}
	require.ErrorIs(t, srv.serve(&flakyListener{fails: 9}, &ssh.ServerConfig{}), net.ErrClosed)
	require.Equal(t, time.Second, longest)
}

type sftpRunAuthClient struct {
	remote.Client
	requests chan remote.SftpAuthRequest
}

func (c *sftpRunAuthClient) ValidateSftpCredentials(_ context.Context, request remote.SftpAuthRequest) (remote.SftpAuthResponse, error) {
	c.requests <- request
	return remote.SftpAuthResponse{Server: "server-id", User: "user-id", Permissions: []string{"read"}}, nil
}

func TestSFTPServerRunPasswordAndPublicKeyCallbacks(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	client := &sftpRunAuthClient{requests: make(chan remote.SftpAuthRequest, 2)}
	srv := New(server.NewEmptyManager(client))
	srv.BasePath = t.TempDir()
	srv.Listen = "127.0.0.1:0"
	oldListen := listenSFTP
	listenerCh := make(chan net.Listener, 1)
	var listener net.Listener
	listenerClosed := false
	runResult := make(chan error, 1)
	listenSFTP = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			listenerCh <- listener
		}
		return listener, err
	}
	t.Cleanup(func() {
		listenSFTP = oldListen
		if !listenerClosed && listener != nil {
			_ = listener.Close()
			select {
			case err := <-runResult:
				if err != nil {
					t.Errorf("SFTP server returned after its listener closed: %v", err)
				}
			case <-time.After(time.Second):
				t.Error("SFTP server did not stop after its listener closed")
			}
		}
	})

	go func() { runResult <- srv.Run() }()
	select {
	case listener = <-listenerCh:
	case <-time.After(time.Second):
		t.Fatal("SFTP server did not bind its listener")
	}
	connect := func(auth ssh.AuthMethod) {
		t.Helper()
		client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
			User:            "validuser.12345678",
			Auth:            []ssh.AuthMethod{auth},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         time.Second,
		})
		require.NoError(t, err)
		require.NoError(t, client.Close())
	}

	connect(ssh.Password("password"))
	passwordRequest := <-client.requests
	require.Equal(t, remote.SftpAuthPassword, passwordRequest.Type)

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(privateKey)
	require.NoError(t, err)
	connect(ssh.PublicKeys(signer))
	publicKeyRequest := <-client.requests
	require.Equal(t, remote.SftpAuthPublicKey, publicKeyRequest.Type)

	require.NoError(t, listener.Close())
	listenerClosed = true
	select {
	case err := <-runResult:
		require.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("SFTP server did not return after listener close")
	}
}

func TestSFTPServerRunStartsListener(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	oldListen := listenSFTP
	t.Cleanup(func() { listenSFTP = oldListen })
	listenSFTP = func(string, string) (net.Listener, error) { return &flakyListener{}, nil }

	srv := New(server.NewEmptyManager(nil))
	srv.BasePath = t.TempDir()
	srv.Listen = "127.0.0.1:0"
	srv.MaxConnections = 1
	if err := srv.Run(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("expected the listener's close error, got %v", err)
	}
}

type flakyListener struct {
	fails int
}

func (f *flakyListener) Accept() (net.Conn, error) {
	if f.fails > 0 {
		f.fails--
		return nil, errors.New("temporary accept error")
	}
	return nil, net.ErrClosed
}

func (f *flakyListener) Close() error   { return nil }
func (f *flakyListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestMustLogErrorPath(t *testing.T) {
	eh := &eventHandler{
		server: "test-server",
		user:   "test-user",
	}
	eh.MustLog(models.Event("test:event"), FileAction{Entity: "test.txt"})
	// Also test with Target populated
	eh.MustLog(models.Event("test:event"), FileAction{Entity: "test.txt", Target: "target.txt"})
	_ = eh.Log(models.Event("test:event"), FileAction{Entity: "test.txt", Target: "target.txt"})
}

type mockNewChannel struct {
	chType       string
	rejectCalled bool
	acceptErr    error
}

func (m *mockNewChannel) ChannelType() string { return m.chType }
func (m *mockNewChannel) ExtraData() []byte   { return nil }
func (m *mockNewChannel) Accept() (ssh.Channel, <-chan *ssh.Request, error) {
	if m.acceptErr != nil {
		return nil, nil, m.acceptErr
	}
	return nil, nil, errors.New("cannot accept")
}
func (m *mockNewChannel) Reject(reason ssh.RejectionReason, message string) error {
	m.rejectCalled = true
	return nil
}

type mockSSHConn struct {
	net.Conn
}

func (m *mockSSHConn) User() string          { return "user" }
func (m *mockSSHConn) SessionID() []byte     { return []byte("session") }
func (m *mockSSHConn) ClientVersion() []byte { return []byte("SSH-2.0-test") }
func (m *mockSSHConn) ServerVersion() []byte { return []byte("SSH-2.0-test") }
func (m *mockSSHConn) RemoteAddr() net.Addr  { return &net.TCPAddr{} }
func (m *mockSSHConn) LocalAddr() net.Addr   { return &net.TCPAddr{} }
func (m *mockSSHConn) Wait() error           { return nil }
func (m *mockSSHConn) OpenChannel(string, []byte) (ssh.Channel, <-chan *ssh.Request, error) {
	return nil, nil, nil
}
func (m *mockSSHConn) SendRequest(string, bool, []byte) (bool, []byte, error) {
	return true, nil, nil
}

func TestServerHandleChannelBranches(t *testing.T) {
	srv := New(&server.Manager{})
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	sconn := &ssh.ServerConn{
		Conn: &mockSSHConn{c1},
		Permissions: &ssh.Permissions{
			Extensions: map[string]string{
				"uuid": "missing-uuid",
			},
		},
	}

	chans := make(chan ssh.NewChannel, 3)
	// 1. Non-session channel
	ch1 := &mockNewChannel{chType: "direct-tcpip"}
	chans <- ch1

	// 2. Session channel with missing server
	ch2 := &mockNewChannel{chType: "session"}
	chans <- ch2

	// 3. Session channel with error on Accept
	ch3 := &mockNewChannel{chType: "session", acceptErr: errors.New("accept error")}
	chans <- ch3

	close(chans)
	reqs := make(chan *ssh.Request)
	close(reqs)

	err := srv.handle(sconn, chans, reqs)
	if err != nil {
		t.Fatalf("unexpected error from handle: %v", err)
	}
	if !ch1.rejectCalled {
		t.Fatal("expected ch1 to be rejected")
	}
	if !ch2.rejectCalled {
		t.Fatal("expected ch2 to be rejected")
	}

	registered, err := server.New(nil)
	require.NoError(t, err)
	registered.Config().Uuid = "registered-server"
	manager := server.NewEmptyManager(nil)
	manager.Add(registered)
	validServer := New(manager)
	sconn.Permissions.Extensions["uuid"] = "registered-server"
	acceptChans := make(chan ssh.NewChannel, 1)
	acceptChans <- &mockNewChannel{chType: "session", acceptErr: errors.New("accept failed")}
	close(acceptChans)
	reqs = make(chan *ssh.Request)
	close(reqs)
	require.NoError(t, validServer.handle(sconn, acceptChans, reqs))
}
