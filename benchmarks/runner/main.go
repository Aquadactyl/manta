// Command runner benchmarks only the four explicitly provisioned local servers.
package main

import (
	"archive/tar"
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type server struct {
	ID         int    `json:"id"`
	UUID       string `json:"uuid"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Identifier string `json:"identifier"`
	Port       int    `json:"port"`
}
type node struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	Container string   `json:"container"`
	Base      string   `json:"base"`
	Token     string   `json:"token"`
	SFTPPort  int      `json:"sftp_port"`
	DataRoot  string   `json:"data_root"`
	Servers   []server `json:"servers"`
}
type manifest struct {
	Nodes       map[string]node `json:"nodes"`
	PanelOrigin string          `json:"panel_origin"`
	SFTP        struct {
		Username, Password string
		UserUUID           string `json:"user_uuid"`
	} `json:"sftp"`
}
type harness struct {
	manifest
	http   *http.Client
	docker *http.Client
	rows   []map[string]any
	output string
	mu     sync.Mutex
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	phase := flag.String("phase", "lifecycle", "lifecycle, performance, security, resources, or events")
	config := flag.String("manifest", "/private/manifest.json", "private manifest")
	output := flag.String("output", "/results", "artifact directory")
	flag.Parse()
	h := &harness{output: filepath.Join(*output, *phase+".json")}
	b, err := os.ReadFile(*config)
	must(err)
	must(json.Unmarshal(b, &h.manifest))
	if len(h.Nodes) != 2 {
		panic("expected exactly two benchmark nodes")
	}
	for key, n := range h.Nodes {
		if (key != "manta" && key != "wings") || len(n.Servers) != 2 {
			panic("unexpected benchmark topology")
		}
		for _, s := range n.Servers {
			if !strings.HasPrefix(s.Name, "Benchmark ") {
				panic("refusing to benchmark an unrelated server")
			}
		}
	}
	h.http = &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{MaxIdleConns: 128, MaxIdleConnsPerHost: 64}}
	h.docker = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}}}
	defer func() {
		if p := recover(); p != nil {
			h.add(map[string]any{"type": "fatal", "error": fmt.Sprint(p)})
			panic(p)
		}
	}()
	h.metadata()
	switch *phase {
	case "lifecycle":
		h.lifecycle()
	case "performance":
		h.performance()
	case "security":
		h.security()
	case "resources":
		h.resources()
	case "events":
		h.eventStreams()
	default:
		panic("unknown phase")
	}
	fmt.Printf("Completed %s: %s\n", *phase, h.output)
}
func (h *harness) add(row map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rows = append(h.rows, row)
	b, err := json.MarshalIndent(h.rows, "", "  ")
	must(err)
	must(os.WriteFile(h.output, b, 0o600))
	if name, ok := row["test"]; ok {
		fmt.Printf("%s %v\n", name, row["variant"])
	}
}
func (h *harness) request(n node, method, path string, body []byte, chunked, auth bool) (int, []byte, error) {
	r, err := http.NewRequest(method, n.Base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if chunked {
		r.ContentLength = -1
		r.TransferEncoding = []string{"chunked"}
	}
	if auth {
		r.Header.Set("Authorization", "Bearer "+n.Token)
	}
	r.Header.Set("Content-Type", "application/json")
	res, err := h.http.Do(r)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s failed (%T)", method, strings.Split(path, "?")[0], err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	return res.StatusCode, b, err
}
func (h *harness) api(n node, method, path string, v any) []byte {
	var b []byte
	if v != nil {
		var err error
		b, err = json.Marshal(v)
		must(err)
	}
	status, out, err := h.request(n, method, path, b, false, true)
	must(err)
	if status < 200 || status >= 300 {
		panic(fmt.Sprintf("%s %s: status %d: %.160s", method, path, status, out))
	}
	return out
}
func (h *harness) dk(path string) []byte {
	res, err := h.docker.Get("http://docker" + path)
	must(err)
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	must(err)
	if res.StatusCode != 200 {
		panic(fmt.Sprintf("Docker %s: status %d", path, res.StatusCode))
	}
	return b
}
func (h *harness) metadata() {
	for k, n := range h.Nodes {
		var ins struct {
			Image      string
			Config     struct{ Env []string }
			HostConfig struct{ NanoCpus, Memory, MemorySwap int64 }
		}
		must(json.Unmarshal(h.dk("/containers/"+n.Container+"/json"), &ins))
		tr := tar.NewReader(bytes.NewReader(h.dk("/containers/" + n.Container + "/archive?path=/usr/bin/wings")))
		_, err := tr.Next()
		must(err)
		bin, err := io.ReadAll(tr)
		must(err)
		info, err := buildinfo.Read(bytes.NewReader(bin))
		must(err)
		gomax := "default"
		for _, e := range ins.Config.Env {
			if strings.HasPrefix(e, "GOMAXPROCS=") {
				gomax = strings.TrimPrefix(e, "GOMAXPROCS=")
			}
		}
		var system any
		must(json.Unmarshal(h.api(n, "GET", "/api/system", nil), &system))
		h.add(map[string]any{"type": "metadata", "variant": k, "image_id": ins.Image, "go_version": info.GoVersion, "gomaxprocs": gomax, "cpu_limit": float64(ins.HostConfig.NanoCpus) / 1e9, "memory_limit": ins.HostConfig.Memory, "system": system, "servers": n.Servers})
	}
}
func (h *harness) find(n node, kind string) server {
	for _, s := range n.Servers {
		if s.Kind == kind {
			return s
		}
	}
	panic("missing game")
}
func (h *harness) state(n node, s server) string {
	var data struct{ State string }
	must(json.Unmarshal(h.api(n, "GET", "/api/servers/"+s.UUID, nil), &data))
	return data.State
}
func (h *harness) power(n node, s server, action string) time.Duration {
	start := time.Now()
	h.api(n, "POST", "/api/servers/"+s.UUID+"/power", map[string]any{"action": action, "wait_seconds": 30})
	want := "running"
	if action == "stop" {
		want = "offline"
	}
	for time.Since(start) < 120*time.Second {
		if h.state(n, s) == want {
			return time.Since(start)
		}
		time.Sleep(100 * time.Millisecond)
	}
	panic(fmt.Sprintf("%s %s timed out", s.Name, action))
}
func (h *harness) commands(n node, s server, commands ...string) {
	h.api(n, "POST", "/api/servers/"+s.UUID+"/commands", map[string]any{"commands": commands})
}
func (h *harness) startAll() {
	for _, variant := range []string{"manta", "wings"} {
		n := h.Nodes[variant]
		for _, s := range n.Servers {
			if h.state(n, s) != "running" {
				h.power(n, s, "start")
			}
		}
	}
}
func (h *harness) lifecycle() {
	for _, kind := range []string{"bedrock", "paper"} {
		for round := 0; round < 4; round++ {
			order := []string{"manta", "wings"}
			if round%2 == 1 {
				order = []string{"wings", "manta"}
			}
			for _, k := range order {
				n := h.Nodes[k]
				s := h.find(n, kind)
				if h.state(n, s) != "offline" {
					h.power(n, s, "stop")
				}
				started := h.power(n, s, "start")
				time.Sleep(2 * time.Second)
				stopped := h.power(n, s, "stop")
				h.add(map[string]any{"type": "measurement", "test": "lifecycle", "variant": k, "game": kind, "round": round, "cold": round == 0, "start_ms": float64(started.Microseconds()) / 1000, "stop_ms": float64(stopped.Microseconds()) / 1000})
			}
		}
	}
	h.startAll()
}
func (h *harness) sftp(n node, s server) (*sftp.Client, *ssh.Client) {
	sc, c, err := h.sftpSuffix(n, s.UUID[:8])
	must(err)
	return sc, c
}

func (h *harness) sftpSuffix(n node, suffix string) (*sftp.Client, *ssh.Client, error) {
	u, _ := url.Parse(n.Base)
	c, err := ssh.Dial("tcp", net.JoinHostPort(u.Hostname(), fmt.Sprint(n.SFTPPort)), &ssh.ClientConfig{
		User: h.SFTP.Username + "." + suffix, Auth: []ssh.AuthMethod{ssh.Password(h.SFTP.Password)}, Timeout: 10 * time.Second,
		//nolint:gosec // Disposable local test nodes; no production credentials or SSH hosts.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return nil, nil, err
	}
	sc, err := sftp.NewClient(c)
	if err != nil {
		_ = c.Close()
		return nil, nil, err
	}
	return sc, c, nil
}
func distribution(v []float64) map[string]any {
	sort.Float64s(v)
	if len(v) == 0 {
		return map[string]any{}
	}
	sum := 0.0
	for _, x := range v {
		sum += x
	}
	return map[string]any{"n": len(v), "mean_ms": sum / float64(len(v)), "p50_ms": v[(len(v)-1)/2], "p95_ms": v[int(float64(len(v)-1)*.95)], "p99_ms": v[int(float64(len(v)-1)*.99)]}
}
