package server

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/apex/log"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/server/filesystem"
)

var resourceEventFixture = []byte(`{"Topic":"resources","Data":{"memory_bytes":624234496,"memory_limit_bytes":2147483648,"cpu_absolute":12.763,"network":{"rx_bytes":381249,"tx_bytes":927156},"uptime":7365124}}`)

func newResourceBenchmarkServer(b testing.TB) *Server {
	b.Helper()
	previousLog := log.Log
	log.Log = &log.Logger{Level: log.InfoLevel, Handler: log.HandlerFunc(func(*log.Entry) error { return nil })}
	b.Cleanup(func() { log.Log = previousLog })
	cfg, err := config.NewAtPath(filepath.Join(b.TempDir(), "config.yml"))
	if err != nil {
		b.Fatal(err)
	}
	cfg.AuthenticationToken = "benchmark-token"
	cfg.System.DiskCheckInterval = 0
	config.Set(cfg)
	s, err := New(nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(s.CleanupForDestroy)
	s.Environment = &stateEnvironment{state: environment.ProcessRunningState}
	s.resources.State.Store(environment.ProcessRunningState)
	s.fs, err = filesystem.New(b.TempDir(), 0, nil)
	if err != nil {
		b.Fatal(err)
	}
	return s
}

// Measure the actual resource-event handler: decoding, resource update, cached
// disk check and optional stats publication. Buffered/drained channels model
// ready observers. Excludes Docker transport, goroutine scheduling and sockets.
func BenchmarkEnvironmentResourceHandling(b *testing.B) {
	for _, count := range []int{0, 1, 16} {
		b.Run(fmt.Sprintf("observers_%d", count), func(b *testing.B) {
			s := newResourceBenchmarkServer(b)
			channels := make([]chan []byte, count)
			for i := range channels {
				channels[i] = make(chan []byte, 1)
				s.Events().On(channels[i])
			}
			limit := newDiskLimiter(s)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				s.handleEnvironmentEvent(resourceEventFixture, limit)
				for _, c := range channels {
					<-c
				}
			}
		})
	}
}

func BenchmarkEnvironmentStateHandling(b *testing.B) {
	s := newResourceBenchmarkServer(b)
	limit := newDiskLimiter(s)
	data := []byte(`{"Topic":"state change","Data":"running"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.handleEnvironmentEvent(data, limit)
	}
}
