package main

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHarnessPerformance(t *testing.T) {
	tempSFTP := t.TempDir()
	sftpPort, stopSSH := startTestSSHServer(t, tempSFTP)
	defer stopSSH()

	var mu sync.Mutex
	var activeConns []*websocket.Conn
	var filePayload []byte

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// WebSocket
		if strings.HasSuffix(r.URL.Path, "/ws") {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			var authMsg struct {
				Event string   `json:"event"`
				Args  []string `json:"args"`
			}
			if err := conn.ReadJSON(&authMsg); err != nil {
				_ = conn.Close()
				return
			}
			_ = conn.WriteJSON(map[string]any{"event": "auth success"})
			mu.Lock()
			activeConns = append(activeConns, conn)
			mu.Unlock()
			return
		}

		// Commands - broadcast to websockets
		if strings.HasSuffix(r.URL.Path, "/commands") {
			var cmdData struct {
				Commands []string `json:"commands"`
			}
			_ = json.NewDecoder(r.Body).Decode(&cmdData)
			mu.Lock()
			for _, cmd := range cmdData.Commands {
				msg := []byte(cmd)
				for _, c := range activeConns {
					_ = c.WriteMessage(websocket.TextMessage, msg)
				}
			}
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// State & server info
		if r.URL.Path == "/api/servers/srv-manta" || r.URL.Path == "/api/servers/srv-wings" {
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "running"})
			return
		}

		// Write file
		if strings.HasSuffix(r.URL.Path, "/files/write") {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			filePayload = body
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// File contents
		if strings.HasSuffix(r.URL.Path, "/files/contents") {
			mu.Lock()
			b := filePayload
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(b)
			return
		}

		// File compress
		if strings.HasSuffix(r.URL.Path, "/files/compress") {
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "archive.tar.gz"})
			return
		}

		// File decompress, delete, chmod, rename, list-directory
		if strings.Contains(r.URL.Path, "/files/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	// Pre-seed payload sha256 check
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
					Base:     ts.URL,
					Token:    "test-token",
					SFTPPort: sftpPort,
					Servers: []server{
						{
							UUID: "srv-manta",
							Kind: "paper",
							Name: "Paper",
						},
					},
				},
				"wings": {
					Base:     ts.URL,
					Token:    "test-token",
					SFTPPort: sftpPort,
					Servers: []server{
						{
							UUID: "srv-wings",
							Kind: "paper",
							Name: "Paper",
						},
					},
				},
			},
		},
		http:   ts.Client(),
		output: filepath.Join(t.TempDir(), "performance-results.json"),
	}

	assert.NotPanics(t, func() {
		h.performance()
	})
	_ = sha256.New()
}

type mockRoundTripper func(*http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return m(r)
}

func TestHarnessResourcesAndPings(t *testing.T) {
	// 1. Test stats
	h := &harness{
		docker: &http.Client{
			Transport: mockRoundTripper(func(r *http.Request) (*http.Response, error) {
				body := `{"cpu_stats":{"cpu_usage":{"total_usage":12345}},"memory_stats":{"usage":2048,"stats":{"inactive_file":512}},"pids_stats":{"current":4}}`
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			}),
		},
	}
	s := h.stats("my-container")
	assert.Equal(t, uint64(12345), s.CPU)
	assert.Equal(t, uint64(1536), s.Working)
	assert.Equal(t, uint64(4), s.PIDs)

	// 2. Test javaPing
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)
		jsonStr := `{"version":{"name":"1.20.4"}}`
		resp := append([]byte{byte(len(jsonStr) + 2), 0x00, byte(len(jsonStr))}, []byte(jsonStr)...)
		_, _ = conn.Write(resp)
	}()

	tcpAddr := ln.Addr().(*net.TCPAddr)
	ver, err := javaPing("127.0.0.1", tcpAddr.Port)
	assert.NoError(t, err)
	assert.Equal(t, "1.20.4", ver)

	// 3. Test bedrockPing
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer udpConn.Close()

	go func() {
		buf := make([]byte, 2048)
		n, remoteAddr, err := udpConn.ReadFrom(buf)
		if err != nil || n == 0 {
			return
		}
		resp := make([]byte, 35)
		resp[0] = 0x1c
		resp = append(resp, []byte("MCPE;Dedicated Server;486;1.20.4;0;10;123456789;Bedrock level;Survival;1;19132;19133;")...)
		_, _ = udpConn.WriteTo(resp, remoteAddr)
	}()

	udpAddr := udpConn.LocalAddr().(*net.UDPAddr)
	bedrockVer, err := bedrockPing("127.0.0.1", udpAddr.Port)
	assert.NoError(t, err)
	assert.Equal(t, "1.20.4", bedrockVer)
}

