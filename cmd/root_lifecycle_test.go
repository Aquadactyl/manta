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
	lrDir := t.TempDir()
	config.SetLogrotateDirectory(lrDir)
	defer config.SetLogrotateDirectory("/etc/logrotate.d")

	cfg.System.Data = filepath.Join(tmpDir, "data")
	cfg.System.ArchiveDirectory = filepath.Join(tmpDir, "archives")
	cfg.System.BackupDirectory = filepath.Join(tmpDir, "backups")
	cfg.System.TmpDirectory = filepath.Join(tmpDir, "tmp")
	cfg.System.Username = "pterodactyl"
	cfg.System.User.Rootless.Enabled = true
	cfg.System.Passwd.Enable = false
	cfg.System.MachineID.Enable = false
	cfg.System.EnableLogRotate = false
	cfg.System.Timezone = "UTC"
	cfg.System.Sftp.Address = "127.0.0.1"
	cfg.System.Sftp.Port = 20225
	cfg.Docker.Network.Mode = "bridge"
	cfg.Docker.Network.Name = "bridge"
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

func TestExitWithConfigurationNoticeUsesExitFunction(t *testing.T) {
	old := exitCommand
	t.Cleanup(func() { exitCommand = old })
	exitCommand = func(code int) { panic(code) }
	require.PanicsWithValue(t, 1, exitWithConfigurationNotice)
}

func TestRootPreRunRejectsAutoTLSWithoutHostname(t *testing.T) {
	oldPath, oldExit := configPath, exitCommand
	oldAutoTLS, oldHostname := rootCommand.Flags().Lookup("auto-tls").Value.String(), rootCommand.Flags().Lookup("tls-hostname").Value.String()
	t.Cleanup(func() {
		configPath, exitCommand = oldPath, oldExit
		_ = rootCommand.Flags().Set("auto-tls", oldAutoTLS)
		_ = rootCommand.Flags().Set("tls-hostname", oldHostname)
	})

	path := filepath.Join(t.TempDir(), "config.yml")
	cfg, err := config.NewAtPath(path)
	require.NoError(t, err)
	cfg.System.LogDirectory = filepath.Join(t.TempDir(), "logs")
	require.NoError(t, config.WriteToDisk(cfg))
	configPath = path
	require.NoError(t, rootCommand.Flags().Set("auto-tls", "true"))
	require.NoError(t, rootCommand.Flags().Set("tls-hostname", ""))
	exitCommand = func(code int) { panic(code) }
	require.PanicsWithValue(t, 1, func() { rootCommand.PreRun(rootCommand, nil) })
}

func TestConnectionLimitsCoversResourceLimitRanges(t *testing.T) {
	old := getNoFileLimit
	t.Cleanup(func() { getNoFileLimit = old })

	t.Run("getrlimit error", func(t *testing.T) {
		getNoFileLimit = func(int, *syscall.Rlimit) error { return syscall.EPERM }
		httpLimit, sftpLimit := connectionLimits()
		assert.Equal(t, 3072, httpLimit)
		assert.Equal(t, 1024, sftpLimit)
	})
	t.Run("low soft limit", func(t *testing.T) {
		getNoFileLimit = func(_ int, r *syscall.Rlimit) error { r.Cur = 512; return nil }
		httpLimit, sftpLimit := connectionLimits()
		assert.Equal(t, 192, httpLimit)
		assert.Equal(t, 64, sftpLimit)
	})
	t.Run("very high soft limit is capped", func(t *testing.T) {
		getNoFileLimit = func(_ int, r *syscall.Rlimit) error { r.Cur = 2 << 20; return nil }
		httpLimit, sftpLimit := connectionLimits()
		assert.Equal(t, 785664, httpLimit)
		assert.Equal(t, 261888, sftpLimit)
	})
}

func TestInitConfigAppliesDebugFlag(t *testing.T) {
	oldPath, oldDebug := configPath, debug
	t.Cleanup(func() { configPath, debug = oldPath, oldDebug })
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg, err := config.NewAtPath(path)
	require.NoError(t, err)
	cfg.Debug = false
	require.NoError(t, config.WriteToDisk(cfg))
	configPath, debug = path, true
	initConfig()
	require.True(t, config.Get().Debug)
}

func TestInitLoggingEnablesDebugLevel(t *testing.T) {
	cfg := &config.Configuration{Debug: true}
	cfg.System.LogDirectory = t.TempDir()
	config.Set(cfg)
	initLogging()
	require.FileExists(t, filepath.Join(cfg.System.LogDirectory, "wings.log"))
}
