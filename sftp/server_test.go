package sftp

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/models"
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
}
