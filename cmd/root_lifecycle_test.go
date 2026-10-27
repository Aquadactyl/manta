package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

func TestRootCmdRunIgnoreCertErrorsDeprecated(t *testing.T) {
	cmd := rootCommand
	_ = cmd.Flags().Set("ignore-certificate-errors", "true")
	defer func() {
		_ = cmd.Flags().Set("ignore-certificate-errors", "false")
	}()

	// The flag logs Fatal, but does not panic
	// Note: log.Fatal exits, so we only test flag reading or deprecated note
	dep := cmd.Flags().Lookup("ignore-certificate-errors")
	assert.NotNil(t, dep)
	assert.NotEmpty(t, dep.Deprecated)
}

func TestRootCmdRunLifecycle(t *testing.T) {
	existingContainerUUID := "82595161-eed8-4536-8f50-689b2c2912bb"
	settingsMap := map[string]any{
		"uuid":             existingContainerUUID,
		"skip_egg_scripts": true,
		"build":            map[string]any{"memory_limit": 128, "disk_space": 128},
		"container":        map[string]any{"image": "~alpine:latest"},
	}
	serverJSON, _ := json.Marshal(map[string]any{
		"uuid":                  existingContainerUUID,
		"settings":              settingsMap,
		"process_configuration": map[string]any{},
	})

	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/remote/servers":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []json.RawMessage{serverJSON},
				"meta": map[string]any{"current_page": 1, "last_page": 1, "total": 1},
			})
		case "/api/remote/servers/reset":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer panel.Close()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	cfg, err := config.NewAtPath(cfgPath)
	require.NoError(t, err)

	cfg.PanelLocation = panel.URL
	cfg.AuthenticationTokenId = "test-id"
	cfg.AuthenticationToken = "test-token"
	require.NoError(t, cfg.ResolveToken(false))
	cfg.System.RootDirectory = tmpDir
	cfg.System.LogDirectory = filepath.Join(tmpDir, "logs")
	cfg.System.Data = filepath.Join(tmpDir, "data")
	cfg.System.ArchiveDirectory = filepath.Join(tmpDir, "archives")
	cfg.System.BackupDirectory = filepath.Join(tmpDir, "backups")
	cfg.System.TmpDirectory = filepath.Join(tmpDir, "tmp")
	cfg.System.Username = "pterodactyl"
	cfg.System.User.Rootless.Enabled = true
	cfg.System.Passwd.Enable = false
	cfg.System.MachineID.Enable = false
	cfg.System.Timezone = "UTC"
	cfg.System.Sftp.Address = "127.0.0.1"
	cfg.System.Sftp.Port = 20225
	cfg.Docker.Network.Mode = "bridge"
	cfg.Api.Host = "127.0.0.1"
	cfg.Api.Port = 0
	cfg.Api.Ssl.Enabled = false
	require.NoError(t, config.WriteToDisk(cfg))
	config.Set(cfg)

	origConfigPath := configPath
	configPath = cfgPath
	defer func() { configPath = origConfigPath }()

	go func() {
		// Wait for server to bind and start serving, then signal SIGTERM
		time.Sleep(500 * time.Millisecond)
		p, err := os.FindProcess(os.Getpid())
		if err == nil {
			_ = p.Signal(syscall.SIGTERM)
		}
	}()

	cmd := rootCommand
	cmd.SetContext(context.Background())
	_ = cmd.Flags().Set("pprof", "true")
	_ = cmd.Flags().Set("pprof-port", "6062")
	_ = cmd.Flags().Set("pprof-block-rate", "10")
	_ = cmd.Flags().Set("auto-tls", "true")
	_ = cmd.Flags().Set("tls-hostname", "")
	defer func() {
		_ = cmd.Flags().Set("pprof", "false")
		_ = cmd.Flags().Set("auto-tls", "false")
	}()

	assert.NotPanics(t, func() {
		rootCmdRun(cmd, nil)
	})
}
