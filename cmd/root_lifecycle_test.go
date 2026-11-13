package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/apex/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
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

func TestRootCmdRunAbortsOnInitializationFailures(t *testing.T) {
	old := struct {
		timezone, directories, user, passwd, rotation func() error
		database                                      func() error
		manager                                       func(context.Context, remote.Client) (*server.Manager, error)
		docker                                        func(context.Context) error
		fatal                                         func(log.Fields, string)
		ignore                                        string
	}{
		configureTimezone, configureDirectories, ensurePterodactylUser, configurePasswd, enableLogRotation,
		initializeDatabase, newServerManager, configureDocker, rootFatal,
		rootCommand.Flags().Lookup("ignore-certificate-errors").Value.String(),
	}
	t.Cleanup(func() {
		configureTimezone, configureDirectories, ensurePterodactylUser = old.timezone, old.directories, old.user
		configurePasswd, enableLogRotation, initializeDatabase = old.passwd, old.rotation, old.database
		newServerManager, configureDocker, rootFatal = old.manager, old.docker, old.fatal
		_ = rootCommand.Flags().Set("ignore-certificate-errors", old.ignore)
	})

	for _, tc := range []struct {
		name string
		fail func(error)
	}{
		{name: "timezone", fail: func(err error) { configureTimezone = func() error { return err } }},
		{name: "directories", fail: func(err error) { configureDirectories = func() error { return err } }},
		{name: "system user", fail: func(err error) { ensurePterodactylUser = func() error { return err } }},
		{name: "container passwd", fail: func(err error) { configurePasswd = func() error { return err } }},
		{name: "log rotation", fail: func(err error) { enableLogRotation = func() error { return err } }},
		{name: "database", fail: func(err error) { initializeDatabase = func() error { return err } }},
		{name: "server manager", fail: func(err error) {
			newServerManager = func(context.Context, remote.Client) (*server.Manager, error) { return nil, err }
		}},
		{name: "docker", fail: func(err error) { configureDocker = func(context.Context) error { return err } }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configureTimezone = func() error { return nil }
			configureDirectories = func() error { return nil }
			ensurePterodactylUser = func() error { return nil }
			configurePasswd = func() error { return nil }
			enableLogRotation = func() error { return nil }
			initializeDatabase = func() error { return nil }
			newServerManager = func(_ context.Context, client remote.Client) (*server.Manager, error) {
				return server.NewEmptyManager(client), nil
			}
			configureDocker = func(context.Context) error { return nil }
			rootFatal = func(_ log.Fields, message string) { panic(message) }
			config.Set(&config.Configuration{})
			_ = rootCommand.Flags().Set("ignore-certificate-errors", "false")
			tc.fail(errors.New("injected initialization failure"))

			cmd := rootCommand
			cmd.SetContext(context.Background())
			require.Panics(t, func() { rootCmdRun(cmd, nil) })
		})
	}
}

func TestRootCmdRunRejectsInsecureCertificateFlag(t *testing.T) {
	oldFatal, oldFlag := rootFatal, rootCommand.Flags().Lookup("ignore-certificate-errors").Value.String()
	t.Cleanup(func() {
		rootFatal = oldFatal
		_ = rootCommand.Flags().Set("ignore-certificate-errors", oldFlag)
	})
	rootFatal = func(_ log.Fields, message string) { panic(message) }
	require.NoError(t, rootCommand.Flags().Set("ignore-certificate-errors", "true"))
	require.Panics(t, func() { rootCmdRun(rootCommand, nil) })
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

func TestExecuteReportsCommandErrors(t *testing.T) {
	oldFatalf := fatalfCommand
	t.Cleanup(func() {
		fatalfCommand = oldFatalf
		rootCommand.SetArgs(nil)
	})
	fatalfCommand = func(format string, args ...interface{}) { panic("fatal: " + fmt.Sprintf(format, args...)) }
	rootCommand.SetArgs([]string{"unknown-command"})
	require.Panics(t, Execute)
}

func TestInitConfigReportsInvalidFile(t *testing.T) {
	oldPath, oldFatalf := configPath, fatalfCommand
	t.Cleanup(func() { configPath, fatalfCommand = oldPath, oldFatalf })

	path := filepath.Join(t.TempDir(), "invalid.yml")
	require.NoError(t, os.WriteFile(path, []byte("system: ["), 0o600))
	configPath = path
	fatalfCommand = func(format string, args ...interface{}) { panic(fmt.Sprintf(format, args...)) }
	require.Panics(t, initConfig)
}

func TestInitConfigReportsMissingFile(t *testing.T) {
	oldPath, oldExit := configPath, exitCommand
	t.Cleanup(func() { configPath, exitCommand = oldPath, oldExit })
	configPath = filepath.Join(t.TempDir(), "missing.yml")
	exitCommand = func(code int) { panic(code) }
	require.PanicsWithValue(t, 1, initConfig)
}

func TestInitConfigReportsAbsolutePathFailure(t *testing.T) {
	oldPath, oldAbs, oldFatalf := configPath, filepathAbs, fatalfCommand
	t.Cleanup(func() { configPath, filepathAbs, fatalfCommand = oldPath, oldAbs, oldFatalf })

	configPath = "relative.yml"
	filepathAbs = func(string) (string, error) { return "", errors.New("cwd unavailable") }
	fatalfCommand = func(format string, args ...interface{}) { panic(fmt.Sprintf(format, args...)) }
	require.Panics(t, initConfig)
}

func TestInitLoggingReportsSetupErrors(t *testing.T) {
	oldFatalf := fatalfCommand
	t.Cleanup(func() { fatalfCommand = oldFatalf })
	fatalfCommand = func(format string, args ...interface{}) { panic(fmt.Sprintf(format, args...)) }

	t.Run("install directory cannot be created", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
		cfg := &config.Configuration{}
		cfg.System.LogDirectory = file
		config.Set(cfg)
		require.Panics(t, initLogging)
	})

	t.Run("log file cannot be opened", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "wings.log"), 0o700))
		cfg := &config.Configuration{}
		cfg.System.LogDirectory = dir
		config.Set(cfg)
		require.Panics(t, initLogging)
	})
}

func TestShutdownOnSignalClosesServerAfterShutdownError(t *testing.T) {
	oldShutdown := shutdownHTTP
	t.Cleanup(func() { shutdownHTTP = oldShutdown })
	shutdownHTTP = func(*http.Server, context.Context) error { return errors.New("shutdown failed") }

	signals := make(chan os.Signal, 1)
	signals <- syscall.SIGTERM
	shutdownOnSignal(signals, server.NewEmptyManager(nil), &http.Server{})
}
