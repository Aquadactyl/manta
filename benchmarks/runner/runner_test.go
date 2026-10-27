package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
}





