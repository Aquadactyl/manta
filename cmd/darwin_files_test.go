//go:build darwin

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

func TestDarwinOpenLogFile(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "external rotation", true: "internal rotation"}[enabled], func(t *testing.T) {
			config.Set(&config.Configuration{System: config.SystemConfiguration{EnableLogRotate: enabled}})
			p := filepath.Join(t.TempDir(), "wings.log")
			f, err := openLogFile(p)
			require.NoError(t, err)
			_, err = f.Write([]byte("log entry\n"))
			require.NoError(t, err)
			require.NoError(t, f.(interface{ Close() error }).Close())
			contents, err := os.ReadFile(p)
			require.NoError(t, err)
			require.Equal(t, "log entry\n", string(contents))
		})
	}
}

func TestDarwinStartMountCheckGate(t *testing.T) {
	previous := runMountCheck
	t.Cleanup(func() { runMountCheck = previous })

	called := make(chan struct{}, 1)
	runMountCheck = func(context.Context) { called <- struct{}{} }

	config.Set(&config.Configuration{})
	startMountCheck(context.Background())
	select {
	case <-called:
		t.Fatal("mount check ran while disabled")
	case <-time.After(10 * time.Millisecond):
	}

	config.Set(&config.Configuration{Docker: config.DockerConfiguration{MountCheck: true}})
	startMountCheck(context.Background())
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("mount check did not run when enabled")
	}
}
