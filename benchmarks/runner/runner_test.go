package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestMust(t *testing.T) {
	assert.NotPanics(t, func() {
		must(nil)
	})
	assert.Panics(t, func() {
		must(errors.New("test panic"))
	})
}

func TestDistribution(t *testing.T) {
	empty := distribution(nil)
	assert.Empty(t, empty)

	dist := distribution([]float64{10.0, 20.0, 30.0, 40.0, 50.0})
	assert.Equal(t, 5, dist["n"])
	assert.Equal(t, 30.0, dist["mean_ms"])
	assert.Equal(t, 30.0, dist["p50_ms"])
}

func TestHarnessAdd(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "results.json")
	h := &harness{output: outPath}

	h.add(map[string]any{"test": "unit_test", "variant": "manta", "val": 123})
	assert.FileExists(t, outPath)

	b, err := os.ReadFile(outPath)
	require.NoError(t, err)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(b, &rows))
	assert.Len(t, rows, 1)
	assert.Equal(t, "unit_test", rows[0]["test"])
}

func TestHarnessFind(t *testing.T) {
	n := node{
		Servers: []server{
			{Kind: "paper", UUID: "paper-uuid"},
			{Kind: "bedrock", UUID: "bedrock-uuid"},
		},
	}
	h := &harness{}

	found := h.find(n, "paper")
	assert.Equal(t, "paper-uuid", found.UUID)

	assert.Panics(t, func() {
		h.find(n, "unknown")
	})
}

func TestHarnessToken(t *testing.T) {
	h := &harness{
		manifest: manifest{
			PanelOrigin: "http://panel.test",
		},
	}
	h.SFTP.UserUUID = "user-123"

	n := node{Token: "secret-node-token"}
	s := server{UUID: "server-uuid-1"}

	tok := h.token(n, s, "websocket", func(claims map[string]any) {
		claims["custom"] = "value"
	})
	assert.NotEmpty(t, tok)
	assert.Contains(t, tok, ".")
}

func TestHarnessRequestAndApi(t *testing.T) {
	var receivedAuth string
	var receivedChunked bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		if len(r.TransferEncoding) > 0 && r.TransferEncoding[0] == "chunked" {
			receivedChunked = true
		}
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error occurred"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	h := &harness{
		http: ts.Client(),
	}
	n := node{Base: ts.URL, Token: "my-token"}

	// request with auth and chunked
	status, body, err := h.request(n, http.MethodPost, "/test", []byte(`{"hello":"world"}`), true, true)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "Bearer my-token", receivedAuth)
	assert.True(t, receivedChunked)
	assert.Contains(t, string(body), "ok")

	// api success
	res := h.api(n, http.MethodGet, "/test", map[string]string{"foo": "bar"})
	assert.Contains(t, string(res), "ok")

	// api failure panics
	assert.Panics(t, func() {
		h.api(n, http.MethodGet, "/fail", nil)
	})
}

func TestHarnessStateAndPowerAndCommands(t *testing.T) {
	currentState := "offline"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/servers/srv-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"state": currentState})
		case "/api/servers/srv-1/power":
			currentState = "running"
			w.WriteHeader(http.StatusAccepted)
		case "/api/servers/srv-1/commands":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	outPath := filepath.Join(t.TempDir(), "results.json")
	h := &harness{
		http:   ts.Client(),
		output: outPath,
	}
	n := node{Base: ts.URL, Token: "token"}
	s := server{UUID: "srv-1", Name: "Benchmark Paper"}

	// state
	st := h.state(n, s)
	assert.Equal(t, "offline", st)

	// power
	dur := h.power(n, s, "start")
	assert.True(t, dur >= 0)
	assert.Equal(t, "running", currentState)

	// commands
	assert.NotPanics(t, func() {
		h.commands(n, s, "say hello", "help")
	})
}

func TestHarnessStats(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/containers/c1/stats", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cpu_stats": map[string]any{
				"cpu_usage": map[string]any{"total_usage": uint64(5000000)},
			},
			"memory_stats": map[string]any{
				"usage": uint64(1024 * 1024 * 100),
				"stats": map[string]uint64{"inactive_file": uint64(1024 * 1024 * 10)},
			},
			"pids_stats": map[string]any{
				"current": uint64(42),
			},
		})
	}))
	defer ts.Close()

	dockerClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("tcp", ts.Listener.Addr().String())
			},
		},
	}
	h := &harness{
		docker: dockerClient,
	}
	sn := h.stats("c1")
	assert.Equal(t, uint64(5000000), sn.CPU)
	assert.Equal(t, uint64(1024*1024*90), sn.Working)
	assert.Equal(t, uint64(42), sn.PIDs)
}

func TestHarnessStartAll(t *testing.T) {
	currentState := "offline"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"state": currentState})
		} else {
			currentState = "running"
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer ts.Close()

	outPath := filepath.Join(t.TempDir(), "results.json")
	h := &harness{
		http:   ts.Client(),
		output: outPath,
		manifest: manifest{
			Nodes: map[string]node{
				"manta": {
					Base:    ts.URL,
					Token:   "tok",
					Servers: []server{{UUID: "s1", Name: "Benchmark Manta"}},
				},
				"wings": {
					Base:    ts.URL,
					Token:   "tok",
					Servers: []server{{UUID: "s2", Name: "Benchmark Wings"}},
				},
			},
		},
	}

	h.startAll()
	assert.Equal(t, "running", currentState)
}

func TestHarnessLifecycle(t *testing.T) {
	oldDelay := lifecycleDelay
	lifecycleDelay = func(time.Duration) {}
	t.Cleanup(func() { lifecycleDelay = oldDelay })

	states := map[string]string{
		"manta-paper": "running", "manta-bedrock": "offline",
		"wings-paper": "offline", "wings-bedrock": "offline",
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 3 || parts[0] != "api" || parts[1] != "servers" {
			http.NotFound(w, r)
			return
		}
		id := parts[2]
		switch {
		case len(parts) == 3 && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]string{"state": states[id]})
		case len(parts) == 4 && parts[3] == "power" && r.Method == http.MethodPost:
			var body struct {
				Action string `json:"action"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if body.Action == "start" {
				states[id] = "running"
			} else {
				states[id] = "offline"
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	var nodes = make(map[string]node)
	for _, variant := range []string{"manta", "wings"} {
		nodes[variant] = node{
			Base:  ts.URL,
			Token: "token",
			Servers: []server{
				{UUID: variant + "-paper", Name: "Benchmark Paper", Kind: "paper"},
				{UUID: variant + "-bedrock", Name: "Benchmark Bedrock", Kind: "bedrock"},
			},
		}
	}
	h := &harness{
		manifest: manifest{Nodes: nodes},
		http:     ts.Client(),
		output:   filepath.Join(t.TempDir(), "lifecycle.json"),
	}

	h.lifecycle()

	assert.Len(t, h.rows, 16)
	for id, state := range states {
		assert.Equal(t, "running", state, id)
	}
}

func TestHarnessMetadata(t *testing.T) {
	binBytes, err := os.ReadFile(os.Args[0])
	require.NoError(t, err)

	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "wings", Size: int64(len(binBytes))}))
	_, _ = tw.Write(binBytes)
	require.NoError(t, tw.Close())

	tsDocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/json") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Image": "sha256:img123",
				"Config": map[string]any{
					"Env": []string{"GOMAXPROCS=2"},
				},
				"HostConfig": map[string]any{
					"NanoCpus":   int64(2000000000),
					"Memory":     int64(1024 * 1024 * 512),
					"MemorySwap": int64(0),
				},
			})
			return
		}
		if strings.Contains(r.URL.Path, "/archive") {
			_, _ = w.Write(tarBuf.Bytes())
			return
		}
		http.NotFound(w, r)
	}))
	defer tsDocker.Close()

	tsNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "1.0"})
	}))
	defer tsNode.Close()

	dockerClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("tcp", tsDocker.Listener.Addr().String())
			},
		},
	}
	outPath := filepath.Join(t.TempDir(), "results.json")
	h := &harness{
		docker: dockerClient,
		http:   tsNode.Client(),
		output: outPath,
		manifest: manifest{
			Nodes: map[string]node{
				"manta": {
					Container: "c1",
					Base:      tsNode.URL,
					Token:     "tok",
					Servers:   []server{{UUID: "s1", Name: "Benchmark Manta"}},
				},
			},
		},
	}

	h.metadata()
	assert.FileExists(t, outPath)
}

func TestHarnessSocket(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}

		_ = conn.WriteJSON(map[string]any{"event": "auth success"})
	}))
	defer ts.Close()

	h := &harness{
		manifest: manifest{
			PanelOrigin: "http://panel.test",
		},
	}
	n := node{Base: ts.URL, Token: "token"}
	s := server{UUID: "srv-ws"}

	c, event := h.socket(n, s, "test-token")
	assert.Equal(t, "auth success", event)
	if c != nil {
		_ = c.Close()
	}
}

func TestVarintAndReadVar(t *testing.T) {
	cases := []int32{0, 1, 127, 128, 255, 300, 16384, 2097151, -1}
	for _, c := range cases {
		buf := varint(c)
		r := bytes.NewReader(buf)
		val, err := readVar(r)
		require.NoError(t, err)
		assert.Equal(t, int(uint32(c)), val)
	}

	// invalid varint (EOF)
	_, err := readVar(bytes.NewReader(nil))
	assert.Error(t, err)

	// bad varint (> 5 bytes with high bit set)
	_, err = readVar(bytes.NewReader([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80}))
	assert.Error(t, err)
}

func TestJavaPing(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	host, portStr, _ := net.SplitHostPort(l.Addr().String())
	var port int
	n, err := fmt.Sscanf(portStr, "%d", &port)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)

		jsonStr := `{"version":{"name":"1.20.4"}}`
		var body bytes.Buffer
		body.WriteByte(0x00)
		body.Write(varint(int32(len(jsonStr))))
		body.WriteString(jsonStr)

		var resp bytes.Buffer
		resp.Write(varint(int32(body.Len())))
		resp.Write(body.Bytes())

		_, _ = conn.Write(resp.Bytes())
	}()

	ver, err := javaPing(host, port)
	require.NoError(t, err)
	assert.Equal(t, "1.20.4", ver)

	_, err = javaPing("127.0.0.1", 1)
	assert.Error(t, err)
}

func TestBedrockPing(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer pc.Close()

	host, portStr, _ := net.SplitHostPort(pc.LocalAddr().String())
	var port int
	n, err := fmt.Sscanf(portStr, "%d", &port)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	go func() {
		buf := make([]byte, 2048)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if n > 0 {
			resp := make([]byte, 35)
			resp[0] = 0x1c
			status := "MCPE;Dedicated Server;486;1.20.50;0;10;123456789;Bedrock level;Survival;1;19132;19133"
			resp = append(resp, []byte(status)...)
			_, _ = pc.WriteTo(resp, addr)
		}
	}()

	ver, err := bedrockPing(host, port)
	require.NoError(t, err)
	assert.Equal(t, "1.20.50", ver)

	// Short pong response error
	pcShort, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer pcShort.Close()
	_, pShortStr, _ := net.SplitHostPort(pcShort.LocalAddr().String())
	var pShort int
	_, _ = fmt.Sscanf(pShortStr, "%d", &pShort)
	go func() {
		buf := make([]byte, 2048)
		_, addr, _ := pcShort.ReadFrom(buf)
		_, _ = pcShort.WriteTo([]byte{0x1c, 0x01}, addr)
	}()
	_, err = bedrockPing("127.0.0.1", pShort)
	assert.Error(t, err)
}

func TestBedrockPingShortStatus(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer pc.Close()

	port := pc.LocalAddr().(*net.UDPAddr).Port
	go func() {
		buf := make([]byte, 2048)
		_, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		response := append(make([]byte, 35), []byte("MCPE;short")...)
		response[0] = 0x1c
		_, _ = pc.WriteTo(response, addr)
	}()

	status, err := bedrockPing("127.0.0.1", port)
	require.NoError(t, err)
	assert.Equal(t, "MCPE;short", status)
}

func TestHarnessEventStreams(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ws") {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()

			var msg map[string]any
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			_ = conn.WriteJSON(map[string]any{"event": "auth success"})

			for sample := 0; sample < 3; sample++ {
				statsPayload := fmt.Sprintf(`{"state":"running","memory_bytes":%d,"memory_limit_bytes":%d,"uptime":%d,"disk_bytes":%d,"cpu_absolute":10.5,"network":{"rx_bytes":%d,"tx_bytes":%d}}`,
					1000+sample, 2000, 100+sample, 500, 100+sample, 100+sample)
				_ = conn.WriteJSON(map[string]any{
					"event": "stats",
					"args":  []string{statsPayload},
				})
			}
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/servers/") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"state": "running",
				"utilization": map[string]any{
					"state":              "running",
					"memory_bytes":       1000,
					"memory_limit_bytes": 2000,
					"uptime":             100,
					"disk_bytes":         500,
					"cpu_absolute":       10.5,
					"network":            map[string]int{"rx_bytes": 100, "tx_bytes": 100},
				},
			})
			return
		}

		http.NotFound(w, r)
	}))
	defer ts.Close()

	outPath := filepath.Join(t.TempDir(), "results.json")
	h := &harness{
		http:   ts.Client(),
		output: outPath,
		manifest: manifest{
			PanelOrigin: "http://panel.test",
			Nodes: map[string]node{
				"manta": {
					Base:    ts.URL,
					Token:   "tok",
					Servers: []server{{UUID: "s-manta", ID: 1, Kind: "paper"}},
				},
				"wings": {
					Base:    ts.URL,
					Token:   "tok",
					Servers: []server{{UUID: "s-wings", ID: 2, Kind: "paper"}},
				},
			},
		},
	}

	h.eventStreams()
	assert.FileExists(t, outPath)
}

func TestHarnessWebsocketLoad(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	var mu sync.Mutex
	var conns []*websocket.Conn

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ws") {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			var msg map[string]any
			if err := conn.ReadJSON(&msg); err != nil {
				_ = conn.Close()
				return
			}
			_ = conn.WriteJSON(map[string]any{"event": "auth success"})

			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			return
		}

		if strings.HasSuffix(r.URL.Path, "/commands") {
			var body struct {
				Commands []string `json:"commands"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusNoContent)

			mu.Lock()
			activeConns := make([]*websocket.Conn, len(conns))
			copy(activeConns, conns)
			mu.Unlock()

			go func() {
				for _, cmd := range body.Commands {
					for _, c := range activeConns {
						_ = c.WriteMessage(websocket.TextMessage, []byte(cmd))
					}
				}
			}()
			return
		}

		http.NotFound(w, r)
	}))
	defer ts.Close()

	outPath := filepath.Join(t.TempDir(), "results.json")
	h := &harness{
		http:   ts.Client(),
		output: outPath,
		manifest: manifest{
			PanelOrigin: "http://panel.test",
		},
	}
	n := node{Base: ts.URL, Token: "token"}
	s := server{UUID: "s-wsload"}

	h.websocketLoad("manta", n, s, 1)
	assert.FileExists(t, outPath)

	mu.Lock()
	for _, c := range conns {
		_ = c.Close()
	}
	mu.Unlock()
}

func TestHarnessPowerTimesOutWhenStateNeverChanges(t *testing.T) {
	oldTimeout, oldInterval := powerWaitTimeout, powerPollInterval
	powerWaitTimeout = 50 * time.Millisecond
	powerPollInterval = 0
	t.Cleanup(func() { powerWaitTimeout, powerPollInterval = oldTimeout, oldInterval })

	stateChecks := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			stateChecks++
			_ = json.NewEncoder(w).Encode(map[string]string{"state": "offline"})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()

	h := &harness{http: ts.Client()}
	n := node{Base: ts.URL, Token: "token"}
	s := server{UUID: "stuck-server", Name: "stuck server"}
	assert.PanicsWithValue(t, "stuck server start timed out", func() { h.power(n, s, "start") })
	assert.Greater(t, stateChecks, 0)
}

func TestHarnessSftpMethods(t *testing.T) {
	h := &harness{}
	n := node{Base: "http://127.0.0.1", SFTPPort: 1}
	s := server{UUID: "12345678-aaaa-bbbb-cccc-dddddddddddd"}

	// sftpSuffix fails to dial closed port
	sc, c, err := h.sftpSuffix(n, "suffix")
	assert.Error(t, err)
	assert.Nil(t, sc)
	assert.Nil(t, c)

	// sftp panics when dial fails
	assert.Panics(t, func() {
		h.sftp(n, s)
	})
}

func TestSFTPSuffixClosesSSHClientWhenSubsystemIsRejected(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(privateKey)
	require.NoError(t, err)
	serverConfig := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
		return &ssh.Permissions{}, nil
	}}
	serverConfig.AddHostKey(signer)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		serverConn, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
		if err != nil {
			_ = conn.Close()
			return
		}
		go ssh.DiscardRequests(requests)
		for channel := range channels {
			_ = channel.Reject(ssh.UnknownChannelType, "SFTP unavailable")
		}
		_ = serverConn.Close()
	}()

	_, portString, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscanf(portString, "%d", &port)
	require.NoError(t, err)
	h := &harness{}
	h.SFTP.Username = "benchmark"
	h.SFTP.Password = "secret"
	_, clientConn, err := h.sftpSuffix(node{Base: "http://127.0.0.1", SFTPPort: port}, "12345678")
	require.Error(t, err)
	require.Nil(t, clientConn)
}

func TestBenchmarkHostKeyCallback(t *testing.T) {
	// 1. Default fallback callback verifies non-empty key
	cb, err := benchmarkHostKeyCallback()
	require.NoError(t, err)
	assert.Error(t, cb("localhost:2022", nil, nil))

	// 2. Invalid BENCHMARK_SFTP_HOSTKEY returns error
	t.Setenv("BENCHMARK_SFTP_HOSTKEY", "invalid-key-data")
	_, err = benchmarkHostKeyCallback()
	assert.Error(t, err)

	// 3. Valid BENCHMARK_SFTP_HOSTKEY parses fixed host key
	testKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG54lC8fV+qHj4V7qE5o8o6vjF9Y5u9O7Z8l6Q3e1A4/ test@benchmark"
	t.Setenv("BENCHMARK_SFTP_HOSTKEY", testKey)
	cb, err = benchmarkHostKeyCallback()
	require.NoError(t, err)
	assert.NotNil(t, cb)
}

func TestMainScenarios(t *testing.T) {
	origArgs := os.Args
	origCommandLine := flag.CommandLine
	defer func() {
		os.Args = origArgs
		flag.CommandLine = origCommandLine
	}()

	resetFlags := func() {
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	}

	// 1. Missing manifest panics
	os.Args = []string{"runner", "-manifest", "/nonexistent/manifest.json", "-output", t.TempDir()}
	resetFlags()
	assert.Panics(t, func() {
		main()
	})

	// 2b. Exactly two nodes with an unexpected key also violate topology.
	badKeyManifestPath := filepath.Join(t.TempDir(), "bad_node_key.json")
	require.NoError(t, os.WriteFile(badKeyManifestPath, []byte(`{
		"nodes": {
			"manta": {"servers": [{"name": "Benchmark Paper"}, {"name": "Benchmark Bedrock"}]},
			"unexpected": {"servers": [{"name": "Benchmark Paper"}, {"name": "Benchmark Bedrock"}]}
		}
	}`), 0644))
	os.Args = []string{"runner", "-manifest", badKeyManifestPath, "-output", t.TempDir()}
	resetFlags()
	assert.Panics(t, func() { main() })

	// 2. Manifest with invalid node topology panics
	badManifestPath := filepath.Join(t.TempDir(), "bad_manifest.json")
	require.NoError(t, os.WriteFile(badManifestPath, []byte(`{"nodes":{"only_one":{}}}`), 0644))
	os.Args = []string{"runner", "-manifest", badManifestPath, "-output", t.TempDir()}
	resetFlags()
	assert.Panics(t, func() {
		main()
	})

	// 3. Manifest with unexpected server naming panics
	badServerManifestPath := filepath.Join(t.TempDir(), "bad_servers.json")
	require.NoError(t, os.WriteFile(badServerManifestPath, []byte(`{
		"nodes": {
			"manta": {"servers": [{"name": "Production 1"}, {"name": "Production 2"}]},
			"wings": {"servers": [{"name": "Production 1"}, {"name": "Production 2"}]}
		}
	}`), 0644))
	os.Args = []string{"runner", "-manifest", badServerManifestPath, "-output", t.TempDir()}
	resetFlags()
	assert.Panics(t, func() {
		main()
	})

	// 4. Valid topology with unknown phase panics after writing fatal entry
	validManifestPath := filepath.Join(t.TempDir(), "valid_manifest.json")
	outDir := t.TempDir()
	require.NoError(t, os.WriteFile(validManifestPath, []byte(`{
		"nodes": {
			"manta": {"servers": [{"name": "Benchmark Paper", "kind": "paper", "uuid": "u1"}, {"name": "Benchmark Bedrock", "kind": "bedrock", "uuid": "u2"}]},
			"wings": {"servers": [{"name": "Benchmark Paper", "kind": "paper", "uuid": "u3"}, {"name": "Benchmark Bedrock", "kind": "bedrock", "uuid": "u4"}]}
		}
	}`), 0644))
	os.Args = []string{"runner", "-manifest", validManifestPath, "-output", outDir, "-phase", "unknown"}
	resetFlags()
	assert.Panics(t, func() {
		main()
	})
	assert.FileExists(t, filepath.Join(outDir, "unknown.json"))
}

func TestMainDispatchesEveryPhase(t *testing.T) {
	origArgs, origCommandLine := os.Args, flag.CommandLine
	oldMetadata := collectRunnerMetadata
	oldPhases := []func(*harness){runLifecyclePhase, runPerformancePhase, runSecurityPhase, runResourcesPhase, runEventsPhase}
	t.Cleanup(func() {
		os.Args, flag.CommandLine = origArgs, origCommandLine
		collectRunnerMetadata = oldMetadata
		runLifecyclePhase, runPerformancePhase, runSecurityPhase, runResourcesPhase, runEventsPhase = oldPhases[0], oldPhases[1], oldPhases[2], oldPhases[3], oldPhases[4]
	})
	collectRunnerMetadata = func(*harness) {}

	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	manifestJSON := `{"nodes":{"manta":{"servers":[{"name":"Benchmark Paper"},{"name":"Benchmark Bedrock"}]},"wings":{"servers":[{"name":"Benchmark Paper"},{"name":"Benchmark Bedrock"}]}}}`
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifestJSON), 0o600))
	phases := []struct {
		name string
		set  func(func(*harness))
	}{
		{name: "lifecycle", set: func(fn func(*harness)) { runLifecyclePhase = fn }},
		{name: "performance", set: func(fn func(*harness)) { runPerformancePhase = fn }},
		{name: "security", set: func(fn func(*harness)) { runSecurityPhase = fn }},
		{name: "resources", set: func(fn func(*harness)) { runResourcesPhase = fn }},
		{name: "events", set: func(fn func(*harness)) { runEventsPhase = fn }},
	}
	for _, phase := range phases {
		t.Run(phase.name, func(t *testing.T) {
			called := false
			phase.set(func(*harness) { called = true })
			os.Args = []string{"runner", "-manifest", manifestPath, "-output", t.TempDir(), "-phase", phase.name}
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
			main()
			require.True(t, called)
		})
	}

	t.Run("unknown phase records a fatal result", func(t *testing.T) {
		out := t.TempDir()
		os.Args = []string{"runner", "-manifest", manifestPath, "-output", out, "-phase", "unknown"}
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
		assert.PanicsWithValue(t, "unknown phase", main)
		assert.FileExists(t, filepath.Join(out, "unknown.json"))
	})
}

func TestHarnessMoreBranches(t *testing.T) {
	h := &harness{
		http: http.DefaultClient,
	}
	n := node{Base: "http://127.0.0.1:1", Token: "token"}

	// 1. request with invalid method
	_, _, err := h.request(n, "INVALID METHOD\n", "/path", nil, false, false)
	assert.Error(t, err)

	// 2. request with client.Do error (connection refused)
	_, _, err = h.request(n, "GET", "/path", nil, false, false)
	assert.Error(t, err)

	// 3. power with "stop" action
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/power") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/srv-stop") {
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "offline"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	h2 := &harness{
		http: ts.Client(),
	}
	n2 := node{Base: ts.URL, Token: "token"}
	s2 := server{UUID: "srv-stop"}
	dur := h2.power(n2, s2, "stop")
	assert.True(t, dur >= 0)

	// 4. dk non-200 panics
	hDocker := &harness{
		docker: ts.Client(),
	}
	assert.Panics(t, func() {
		hDocker.dk("/notfound")
	})

	// 5. sftpSuffix with invalid BENCHMARK_SFTP_HOSTKEY returns error
	t.Setenv("BENCHMARK_SFTP_HOSTKEY", "invalid-key-data")
	_, _, err = h.sftpSuffix(node{Base: "http://127.0.0.1:8080"}, "test")
	assert.Error(t, err)
}

func TestJavaPingBadPacketLength(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	host, portStr, _ := net.SplitHostPort(l.Addr().String())
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)
		// send length 1 (n < 2)
		_, _ = conn.Write([]byte{0x01})
	}()

	_, err = javaPing(host, port)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bad packet length")
}

func TestJavaPingReadErrorsAndLongStringLength(t *testing.T) {
	serve := func(t *testing.T, response []byte) (string, int) {
		t.Helper()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = l.Close() })
		host, portText, err := net.SplitHostPort(l.Addr().String())
		require.NoError(t, err)
		var port int
		_, err = fmt.Sscanf(portText, "%d", &port)
		require.NoError(t, err)
		go func() {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			buf := make([]byte, 1024)
			_, _ = conn.Read(buf)
			if response != nil {
				_, _ = conn.Write(response)
			}
		}()
		return host, port
	}

	t.Run("missing frame is a read error", func(t *testing.T) {
		host, port := serve(t, nil)
		_, err := javaPing(host, port)
		require.Error(t, err)
	})

	t.Run("truncated frame body is a read error", func(t *testing.T) {
		host, port := serve(t, []byte{4, 0})
		_, err := javaPing(host, port)
		require.Error(t, err)
	})

	t.Run("long JSON length uses continuation varint", func(t *testing.T) {
		jsonText, err := json.Marshal(map[string]any{"version": map[string]string{"name": strings.Repeat("v", 140)}})
		require.NoError(t, err)
		body := append([]byte{0}, varint(int32(len(jsonText)))...)
		body = append(body, jsonText...)
		response := append(varint(int32(len(body))), body...)
		host, port := serve(t, response)
		version, err := javaPing(host, port)
		require.NoError(t, err)
		require.Equal(t, strings.Repeat("v", 140), version)
	})
}

func TestBedrockPingRejectsInvalidPacketID(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port
	go func() {
		buf := make([]byte, 2048)
		_, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		response := make([]byte, 35)
		response[0] = 0x01
		_, _ = pc.WriteTo(response, addr)
	}()
	_, err = bedrockPing("127.0.0.1", port)
	require.EqualError(t, err, "bad RakNet pong")
}

type pingTestConn struct {
	readErr  error
	writeErr error
}

func (c *pingTestConn) Read([]byte) (int, error)         { return 0, c.readErr }
func (c *pingTestConn) Write([]byte) (int, error)        { return 0, c.writeErr }
func (c *pingTestConn) Close() error                     { return nil }
func (c *pingTestConn) LocalAddr() net.Addr              { return &net.IPAddr{} }
func (c *pingTestConn) RemoteAddr() net.Addr             { return &net.IPAddr{} }
func (c *pingTestConn) SetDeadline(time.Time) error      { return nil }
func (c *pingTestConn) SetReadDeadline(time.Time) error  { return nil }
func (c *pingTestConn) SetWriteDeadline(time.Time) error { return nil }

func TestPingDialAndSocketErrors(t *testing.T) {
	t.Run("Java write error", func(t *testing.T) {
		old := dialJavaPing
		t.Cleanup(func() { dialJavaPing = old })
		want := errors.New("write failed")
		dialJavaPing = func(string, string, time.Duration) (net.Conn, error) {
			return &pingTestConn{writeErr: want}, nil
		}
		_, err := javaPing("example.invalid", 25565)
		require.ErrorIs(t, err, want)
	})

	t.Run("Bedrock dial error", func(t *testing.T) {
		old := dialBedrockPing
		t.Cleanup(func() { dialBedrockPing = old })
		want := errors.New("dial failed")
		dialBedrockPing = func(string, string, time.Duration) (net.Conn, error) { return nil, want }
		_, err := bedrockPing("example.invalid", 19132)
		require.ErrorIs(t, err, want)
	})

	t.Run("Bedrock write error", func(t *testing.T) {
		old := dialBedrockPing
		t.Cleanup(func() { dialBedrockPing = old })
		want := errors.New("write failed")
		dialBedrockPing = func(string, string, time.Duration) (net.Conn, error) {
			return &pingTestConn{writeErr: want}, nil
		}
		_, err := bedrockPing("example.invalid", 19132)
		require.ErrorIs(t, err, want)
	})

	t.Run("Bedrock read error", func(t *testing.T) {
		old := dialBedrockPing
		t.Cleanup(func() { dialBedrockPing = old })
		want := errors.New("read failed")
		dialBedrockPing = func(string, string, time.Duration) (net.Conn, error) {
			return &pingTestConn{readErr: want}, nil
		}
		_, err := bedrockPing("example.invalid", 19132)
		require.ErrorIs(t, err, want)
	})
}

func TestReadVarRejectsOverlongValues(t *testing.T) {
	_, err := readVar(bytes.NewReader([]byte{0x80, 0x80, 0x80, 0x80, 0x80}))
	require.EqualError(t, err, "bad varint")

	_, err = readVar(bytes.NewReader(nil))
	require.ErrorIs(t, err, io.EOF)
}

func TestDkNon200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	h := &harness{
		docker: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return net.Dial("tcp", u.Host)
				},
			},
		},
	}
	assert.Panics(t, func() {
		h.dk("/bad")
	})
}

func TestSocketNoAuthenticationResult(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var msg map[string]any
		_ = conn.ReadJSON(&msg)
		for i := 0; i < 11; i++ {
			_ = conn.WriteJSON(map[string]any{"event": "other-event"})
		}
	}))
	defer ts.Close()

	h := &harness{manifest: manifest{PanelOrigin: "http://example.com"}}
	n := node{Base: ts.URL}
	s := server{UUID: "test-uuid"}
	conn, res := h.socket(n, s, "token")
	if conn != nil {
		_ = conn.Close()
	}
	assert.Equal(t, "no authentication result", res)
}

func TestHarnessPowerAndCommands(t *testing.T) {
	state := "offline"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/power") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["action"] == "start" {
				state = "running"
			} else {
				state = "offline"
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/commands") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// GET /api/servers/:uuid
		_ = json.NewEncoder(w).Encode(map[string]any{
			"state":       state,
			"utilization": map[string]any{},
		})
	}))
	defer ts.Close()

	n := node{Base: ts.URL}
	s := server{UUID: "srv-uuid", Name: "test-server"}
	h := &harness{
		http: ts.Client(),
		manifest: manifest{
			PanelOrigin: "http://example.com",
			Nodes: map[string]node{
				"manta": {Base: ts.URL, Servers: []server{s}},
				"wings": {Base: ts.URL, Servers: []server{s}},
			},
		},
	}

	h.commands(n, s, "say hello")
	dur := h.power(n, s, "start")
	assert.True(t, dur >= 0)
	dur = h.power(n, s, "stop")
	assert.True(t, dur >= 0)

	h.startAll()
}
