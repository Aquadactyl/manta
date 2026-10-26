package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/sftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func startTestSSHServer(t *testing.T, sftpDir string) (int, func()) {
	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port

	stop := make(chan struct{})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-stop:
					return
				default:
					return
				}
			}
			go func(c net.Conn) {
				defer c.Close()
				sConn, chans, reqs, err := ssh.NewServerConn(c, config)
				if err != nil {
					return
				}
				defer sConn.Close()
				go ssh.DiscardRequests(reqs)
				for newChannel := range chans {
					if newChannel.ChannelType() != "session" {
						_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
						continue
					}
					channel, requests, err := newChannel.Accept()
					if err != nil {
						return
					}
					go func(in <-chan *ssh.Request) {
						defer channel.Close()
						for req := range in {
							if req.Type == "subsystem" && bytes.Contains(req.Payload, []byte("sftp")) {
								_ = req.Reply(true, nil)
								server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(sftpDir))
								if err == nil {
									_ = server.Serve()
								}
								return
							}
							_ = req.Reply(false, nil)
						}
					}(requests)
				}
			}(conn)
		}
	}()

	return port, func() {
		close(stop)
		_ = listener.Close()
	}
}

func TestHarnessSecurity(t *testing.T) {
	tempSFTP := t.TempDir()
	sftpPort, stopSSH := startTestSSHServer(t, tempSFTP)
	defer stopSSH()

	var fileMu sync.Mutex
	fileStore := make(map[string][]byte)
	downloadTokens := make(map[string]bool)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// WebSocket check
		if strings.HasSuffix(r.URL.Path, "/ws") {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()

			var authMsg struct {
				Event string   `json:"event"`
				Args  []string `json:"args"`
			}
			if err := conn.ReadJSON(&authMsg); err != nil {
				return
			}

			// Validate token claims
			token := ""
			if len(authMsg.Args) > 0 {
				token = authMsg.Args[0]
			}
			parts := strings.Split(token, ".")
			valid := false
			if len(parts) >= 2 {
				payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
				if err == nil {
					var claims map[string]any
					if err := json.Unmarshal(payloadBytes, &claims); err == nil {
						now := time.Now().Unix()
						exp, hasExp := claims["exp"].(float64)
						iat, hasIat := claims["iat"].(float64)
						nbf, hasNbf := claims["nbf"].(float64)

						if hasExp && int64(exp) > now &&
							(!hasIat || int64(iat) <= now+5) &&
							(!hasNbf || int64(nbf) <= now+5) {
							valid = true
						}
					}
				}
			}

			if valid {
				_ = conn.WriteJSON(map[string]any{"event": "auth success"})
			} else {
				_ = conn.WriteJSON(map[string]any{"event": "jwt error"})
			}
			return
		}

		// Unauthenticated check
		if r.URL.Path == "/api/system" {
			auth := r.Header.Get("Authorization")
			if auth == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"version":"test"}`))
			return
		}

		// Server state
		if strings.HasPrefix(r.URL.Path, "/api/servers/") && !strings.Contains(r.URL.Path, "/files") && !strings.Contains(r.URL.Path, "/power") {
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "running"})
			return
		}

		// Directory create
		if strings.HasSuffix(r.URL.Path, "/files/create-directory") {
			body, _ := io.ReadAll(r.Body)
			if len(body) > 2<<20 || strings.Contains(string(body), "oversize") {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			if strings.Contains(string(body), "multiple-json") {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		// File write
		if strings.HasSuffix(r.URL.Path, "/files/write") {
			filePath := r.URL.Query().Get("file")
			body, _ := io.ReadAll(r.Body)
			fileMu.Lock()
			fileStore[filePath] = body
			fileMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// File contents
		if strings.HasSuffix(r.URL.Path, "/files/contents") {
			filePath := r.URL.Query().Get("file")
			fileMu.Lock()
			content, exists := fileStore[filePath]
			fileMu.Unlock()
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(content)
			return
		}

		// Download file
		if r.URL.Path == "/download/file" {
			token := r.URL.Query().Get("token")
			fileMu.Lock()
			defer fileMu.Unlock()
			if downloadTokens[token] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			downloadTokens[token] = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("file-content"))
			return
		}

		// Upload file
		if r.URL.Path == "/upload/file" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Files delete
		if strings.HasSuffix(r.URL.Path, "/files/delete") {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Files pull
		if strings.HasSuffix(r.URL.Path, "/files/pull") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("blocked ssrf"))
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Docker mock server for h.dk
	dockerTs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("safe logs without marker"))
	}))
	defer dockerTs.Close()

	dockerClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("tcp", dockerTs.Listener.Addr().String())
			},
		},
	}

	h := &harness{
		manifest: manifest{
			PanelOrigin: ts.URL,
			SFTP: struct {
				Username, Password string
				UserUUID           string `json:"user_uuid"`
			}{
				Username: "ptlc_user",
				Password: "password",
				UserUUID: "user-uuid-1",
			},
			Nodes: map[string]node{
				"manta": {
					Base:      ts.URL,
					Token:     "test-token",
					Container: "manta-container",
					SFTPPort:  sftpPort,
					Servers: []server{
						{
							UUID:       "12345678-aaaa-bbbb-cccc-dddddddddddd",
							Identifier: "12345678",
							Kind:       "paper",
							Name:       "Paper",
						},
					},
				},
				"wings": {
					Base:      ts.URL,
					Token:     "test-token",
					Container: "wings-container",
					SFTPPort:  sftpPort,
					Servers: []server{
						{
							UUID:       "87654321-aaaa-bbbb-cccc-dddddddddddd",
							Identifier: "87654321",
							Kind:       "paper",
							Name:       "Paper",
						},
					},
				},
			},
		},
		http:   ts.Client(),
		docker: dockerClient,
		output: filepath.Join(t.TempDir(), "security-results.json"),
	}

	assert.NotPanics(t, func() {
		h.security()
	})
}
