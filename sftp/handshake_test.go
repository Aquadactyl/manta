package sftp

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/pterodactyl/wings/server"
)

type deadlineFailureConn struct {
	net.Conn
	failCall int
	calls    int
	err      error
}

func connectedTCPPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	clientConn, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	serverConn := <-accepted
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})
	return serverConn, clientConn
}

func (c *deadlineFailureConn) SetDeadline(deadline time.Time) error {
	c.calls++
	if c.calls == c.failCall {
		return c.err
	}
	return c.Conn.SetDeadline(deadline)
}

func TestHandshakeDeadlineAndSSHOutcomes(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(privateKey)
	require.NoError(t, err)
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)
	clientConfig := &ssh.ClientConfig{
		User:            "test-user",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	t.Run("first deadline failure", func(t *testing.T) {
		serverConn, _ := connectedTCPPair(t)
		wantErr := errors.New("set deadline failed")
		wrapped := &deadlineFailureConn{Conn: serverConn, failCall: 1, err: wantErr}
		_, _, _, err := handshake(wrapped, serverConfig, 10*time.Second)
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("SSH negotiation failure", func(t *testing.T) {
		serverConn, clientConn := connectedTCPPair(t)
		_ = clientConn.Close()
		_, _, _, err := handshake(serverConn, serverConfig, 10*time.Second)
		require.Error(t, err)
	})

	t.Run("deadline reset failure closes the session", func(t *testing.T) {
		serverConn, clientConn := connectedTCPPair(t)
		wantErr := errors.New("clear deadline failed")
		wrapped := &deadlineFailureConn{Conn: serverConn, failCall: 2, err: wantErr}
		clientResult := make(chan error, 1)
		go func() {
			_, _, _, err := ssh.NewClientConn(clientConn, "pipe", clientConfig)
			clientResult <- err
		}()

		session, _, _, err := handshake(wrapped, serverConfig, 10*time.Second)
		require.ErrorIs(t, err, wantErr)
		require.Nil(t, session)
		require.NoError(t, <-clientResult)
	})

	t.Run("successful handshake clears deadline", func(t *testing.T) {
		serverConn, clientConn := connectedTCPPair(t)
		clientResult := make(chan error, 1)
		go func() {
			_, _, _, err := ssh.NewClientConn(clientConn, "pipe", clientConfig)
			clientResult <- err
		}()

		session, channels, requests, err := handshake(serverConn, serverConfig, 10*time.Second)
		require.NoError(t, err)
		require.NotNil(t, session)
		require.NotNil(t, channels)
		require.NotNil(t, requests)
		require.NoError(t, <-clientResult)
		require.NoError(t, session.Close())
	})
}

func TestAcceptInboundCompletesAuthenticatedSession(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(privateKey)
	require.NoError(t, err)
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)
	clientConfig := &ssh.ClientConfig{
		User:            "test-user",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	serverConn, clientConn := connectedTCPPair(t)
	sftpServer := &SFTPServer{manager: server.NewEmptyManager(nil)}
	acceptResult := make(chan error, 1)
	go func() { acceptResult <- sftpServer.AcceptInbound(serverConn, serverConfig) }()

	serverConnection, channels, requests, err := ssh.NewClientConn(clientConn, "pipe", clientConfig)
	require.NoError(t, err)
	require.NotNil(t, channels)
	require.NotNil(t, requests)
	client := ssh.NewClient(serverConnection, channels, requests)
	// The server's discarded-request handler is started only after handshake has
	// cleared the connection deadline. Wait for its reply before closing so the
	// client cannot race the final SetDeadline call.
	_, _, err = client.SendRequest("keepalive@openssh.com", true, nil)
	require.NoError(t, err)
	require.NoError(t, client.Close())
	require.NoError(t, <-acceptResult)
}
