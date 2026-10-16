package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apex/log"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

// BenchmarkServerStatus measures the full routing/authentication/JSON path,
// excluding the network and Docker. Use the same toolchain for both versions.
func BenchmarkServerStatus(b *testing.B) {
	benchmarkServerStatus(b, 1)
}

// Only the requested server needs a filesystem/environment; the other entries
// model lookup cost without provisioning hundreds of real game containers.
func BenchmarkServerStatusScale(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) { benchmarkServerStatus(b, count) })
	}
}

func benchmarkServerStatus(b *testing.B, count int) {
	benchmarkServerRoute(b, count, 0, 0)
}

func BenchmarkDirectoryAPI(b *testing.B) {
	for _, count := range []int{10, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) { benchmarkServerRoute(b, 1, count, 0) })
	}
}

func BenchmarkFileReadAPI(b *testing.B) {
	for _, size := range []int{1 << 20, 16 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) { benchmarkServerRoute(b, 1, 0, size) })
	}
}

// Discard response bytes without implementing ReaderFrom: the Gin writer must
// perform the same streaming writes as it does for an HTTP client. Excludes
// network transport; live transfers independently verify content hashes.
type fileBenchmarkWriter struct {
	header http.Header
	status int
	bytes  int
	writes int
}

func (w *fileBenchmarkWriter) Header() http.Header    { return w.header }
func (w *fileBenchmarkWriter) WriteHeader(status int) { w.status = status }
func (w *fileBenchmarkWriter) Flush()                 {}
func (w *fileBenchmarkWriter) Write(p []byte) (int, error) {
	w.bytes += len(p)
	w.writes++
	return len(p), nil
}

func benchmarkServerRoute(b *testing.B, count, files, readBytes int) {
	previousLog := log.Log
	log.Log = &log.Logger{Level: log.InfoLevel, Handler: log.HandlerFunc(func(*log.Entry) error { return nil })}
	b.Cleanup(func() { log.Log = previousLog })
	cfg, err := config.NewAtPath(filepath.Join(b.TempDir(), "config.yml"))
	if err != nil {
		b.Fatal(err)
	}
	cfg.AuthenticationToken = "benchmark-token"
	cfg.System.Data = filepath.Join(b.TempDir(), "volumes")
	cfg.System.MachineID.Enable = false
	config.Set(cfg)
	manager := server.NewEmptyManager(nil)
	for i := 0; i < count-1; i++ {
		other, err := server.New(nil)
		if err != nil {
			b.Fatal(err)
		}
		settings, err := json.Marshal(map[string]string{"uuid": fmt.Sprintf("%08x-1111-4111-8111-111111111111", i)})
		if err != nil {
			b.Fatal(err)
		}
		if err := other.SyncWithConfiguration(remote.ServerConfigurationResponse{Settings: settings}); err != nil {
			b.Fatal(err)
		}
		manager.Add(other)
		b.Cleanup(other.CleanupForDestroy)
	}
	settings, err := json.Marshal(map[string]string{"uuid": transferTestUUID})
	if err != nil {
		b.Fatal(err)
	}
	s, err := manager.InitServer(transferTestUUID, remote.ServerConfigurationResponse{
		Settings: settings, ProcessConfiguration: &remote.ProcessConfiguration{},
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(s.CleanupForDestroy)
	s.Environment = transferTestEnvironment{}
	manager.Add(s)
	router := Configure(manager, nil)
	path := "/api/servers/" + s.ID()
	if files > 0 {
		for i := range files {
			if err := os.WriteFile(filepath.Join(s.Filesystem().Path(), fmt.Sprintf("f%04d.txt", i)), []byte(strings.Repeat("x", 256)), 0o600); err != nil {
				b.Fatal(err)
			}
		}
		path += "/files/list-directory?directory=/"
	}
	if readBytes > 0 {
		if err := os.WriteFile(filepath.Join(s.Filesystem().Path(), "payload.bin"), []byte(strings.Repeat("x", readBytes)), 0o600); err != nil {
			b.Fatal(err)
		}
		path += "/files/contents?file=/payload.bin"
		b.SetBytes(int64(readBytes))
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer benchmark-token")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if readBytes > 0 {
			res := &fileBenchmarkWriter{header: make(http.Header)}
			router.ServeHTTP(res, req)
			if res.status != http.StatusOK || res.bytes != readBytes {
				b.Fatalf("status %d, bytes %d", res.status, res.bytes)
			}
			b.ReportMetric(float64(res.writes), "writes/op")
			continue
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			b.Fatalf("status %d", res.Code)
		}
	}
}
