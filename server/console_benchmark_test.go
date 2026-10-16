package server

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/apex/log"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
)

func BenchmarkConsoleInspection(b *testing.B) {
	previousLog := log.Log
	log.Log = &log.Logger{Level: log.InfoLevel, Handler: log.HandlerFunc(func(*log.Entry) error { return nil })}
	b.Cleanup(func() { log.Log = previousLog })
	for _, state := range []string{environment.ProcessRunningState, environment.ProcessStartingState} {
		b.Run(state, func(b *testing.B) {
			cfg, err := config.NewAtPath(filepath.Join(b.TempDir(), "config.yml"))
			if err != nil {
				b.Fatal(err)
			}
			cfg.AuthenticationToken = "benchmark-token"
			config.Set(cfg)
			s, err := New(nil)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(s.CleanupForDestroy)
			s.Environment = &stateEnvironment{state: state}
			s.procConfig = &remote.ProcessConfiguration{}
			line := bytes.Repeat([]byte("console line "), 12)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				s.onConsoleOutput(line)
			}
		})
	}
}
