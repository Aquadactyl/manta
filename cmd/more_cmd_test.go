package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/docker/docker/api/types"
	dockersystem "github.com/docker/docker/api/types/system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
)

func TestConnectionLimits(t *testing.T) {
	httpLim, sftpLim := connectionLimits()
	assert.True(t, httpLim > 0)
	assert.True(t, sftpLim > 0)
}

func TestPrintLogo(t *testing.T) {
	// printLogo prints to stdout without error
	assert.NotPanics(t, func() {
		printLogo()
	})
}

func TestDiagnosticsRedact(t *testing.T) {
	prev := diagnosticsArgs.IncludeEndpoints
	defer func() { diagnosticsArgs.IncludeEndpoints = prev }()

	diagnosticsArgs.IncludeEndpoints = false
	assert.Equal(t, "{redacted}", redact("sensitive-endpoint"))

	diagnosticsArgs.IncludeEndpoints = true
	assert.Equal(t, "sensitive-endpoint", redact("sensitive-endpoint"))
}

func TestDiagnosticsPrintHeader(t *testing.T) {
	var buf bytes.Buffer
	printHeader(&buf, "Test Section")
	assert.Contains(t, buf.String(), "Test Section")
	assert.Contains(t, buf.String(), "------------------------------")
}

func TestDiagnosticsNewCommand(t *testing.T) {
	cmd := newDiagnosticsCommand()
	assert.Equal(t, "diagnostics", cmd.Use)
	assert.NotNil(t, cmd.Flag("hastebin-url"))
	assert.NotNil(t, cmd.Flag("log-lines"))
}

func TestDiagnosticsGetDockerInfo(t *testing.T) {
	v, info, err := getDockerInfo()
	// Even if Docker is not available in some environment, it returns without panic
	if err == nil {
		assert.NotEmpty(t, v.Version)
		assert.NotEmpty(t, info.ServerVersion)
	}
}

func TestUploadToHastebin_Errors(t *testing.T) {
	t.Run("invalid url", func(t *testing.T) {
		_, err := uploadToHastebin("://bad-url", "content")
		assert.Error(t, err)
	})

	t.Run("http 500 status", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer s.Close()

		_, err := uploadToHastebin(s.URL, "content")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected response status")
	})

	t.Run("invalid json", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not json"))
		}))
		defer s.Close()

		_, err := uploadToHastebin(s.URL, "content")
		assert.Error(t, err)
	})

	t.Run("missing key", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"other":"val"}`))
		}))
		defer s.Close()

		_, err := uploadToHastebin(s.URL, "content")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to find key")
	})
}

func TestWriteConfiguration_NonExistent(t *testing.T) {
	prev := configPath
	defer func() { configPath = prev }()

	configPath = filepath.Join(t.TempDir(), "nonexistent.yml")
	var out strings.Builder
	cfg := writeConfiguration(&out)
	assert.Nil(t, cfg)
	assert.Contains(t, out.String(), "Failed to load configuration:")
}

func TestInitConfigAndLogging(t *testing.T) {
	prevConfigPath := configPath
	prevDebug := debug
	defer func() {
		configPath = prevConfigPath
		debug = prevDebug
	}()

	tempDir := t.TempDir()
	p := filepath.Join(tempDir, "config.yml")
	cfg, err := config.NewAtPath(p)
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	cfg.System.LogDirectory = filepath.Join(tempDir, "logs")
	require.NoError(t, config.WriteToDisk(cfg))

	configPath = p
	debug = true

	initConfig()
	assert.True(t, config.Get().Debug)

	// Test initLogging
	initLogging()
	assert.DirExists(t, filepath.Join(tempDir, "logs", "install"))
}

func TestVersionCommand(t *testing.T) {
	assert.Equal(t, "version", versionCommand.Use)
	assert.NotPanics(t, func() {
		versionCommand.Run(versionCommand, nil)
	})
}

func TestWritePanelConfiguration(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yml")
	body := []byte(`{"uuid":"node-123","token_id":"id-1","token":"secret-token","api":{"port":8080}}`)
	err := writePanelConfiguration(cfgPath, "https://panel.example.com", body)
	require.NoError(t, err)
	assert.FileExists(t, cfgPath)

	// Invalid JSON
	assert.Error(t, writePanelConfiguration(cfgPath, "https://panel.example.com", []byte("invalid json")))
}

func TestGetRequest(t *testing.T) {
	configureArgs.PanelURL = "https://panel.example.com"
	configureArgs.Node = "node-123"
	configureArgs.Token = "my-token"
	req, err := getRequest()
	require.NoError(t, err)
	assert.Equal(t, "https://panel.example.com/api/application/nodes/node-123/configuration", req.URL.String())
	assert.Equal(t, "Bearer my-token", req.Header.Get("Authorization"))
}

func TestWriteConfiguration_ExistingFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yml")
	origPath := configPath
	configPath = cfgPath
	defer func() { configPath = origPath }()

	body := []byte(`{"uuid":"node-123","token_id":"id-1","token":"secret-token","api":{"port":8080}}`)
	require.NoError(t, writePanelConfiguration(cfgPath, "https://panel.example.com", body))

	var buf strings.Builder
	cfg := writeConfiguration(&buf)
	assert.NotNil(t, cfg)
	assert.Contains(t, buf.String(), "Panel Location")
}

func TestConfigureCmdRun_NonInteractive(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uuid":"node-123","token_id":"id-1","token":"secret-token","api":{"port":8080}}`))
	}))
	defer ts.Close()

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yml")

	origArgs := configureArgs
	defer func() { configureArgs = origArgs }()

	configureArgs.PanelURL = ts.URL
	configureArgs.Token = "test-token"
	configureArgs.Node = "1"
	configureArgs.ConfigPath = cfgPath
	configureArgs.Override = true
	configureArgs.AllowInsecure = true

	assert.NotPanics(t, func() {
		configureCmdRun(nil, nil)
	})
	assert.FileExists(t, cfgPath)
}

func TestShutdownOnSignal(t *testing.T) {
	mgr := server.NewEmptyManager(nil)
	srv := &http.Server{
		Addr: "127.0.0.1:0",
	}

	sigChan := make(chan os.Signal, 1)
	sigChan <- syscall.SIGTERM

	assert.NotPanics(t, func() {
		shutdownOnSignal(sigChan, mgr, srv)
	})
}

func TestRootCommandHelp(t *testing.T) {
	rootCommand.SetArgs([]string{"--help"})
	assert.NoError(t, rootCommand.Execute())
}

func TestExecuteVersion(t *testing.T) {
	rootCommand.SetArgs([]string{"version"})
	assert.NotPanics(t, func() {
		Execute()
	})
}

func TestRootCommandPreRun(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	content := []byte(`
debug: false
token_id: "test"
token: "test-token"
system:
  root_directory: "` + tmpDir + `"
  log_directory: "` + filepath.Join(tmpDir, "logs") + `"
  data: "` + filepath.Join(tmpDir, "data") + `"
  archive_directory: "` + filepath.Join(tmpDir, "archives") + `"
  backup_directory: "` + filepath.Join(tmpDir, "backups") + `"
  tmp_directory: "` + filepath.Join(tmpDir, "tmp") + `"
api:
  host: "127.0.0.1"
  port: 8080
`)
	require.NoError(t, os.WriteFile(cfgPath, content, 0600))

	oldConfigPath := configPath
	defer func() { configPath = oldConfigPath }()
	configPath = cfgPath

	cmd := rootCommand
	_ = cmd.Flags().Set("auto-tls", "true")
	_ = cmd.Flags().Set("tls-hostname", "test.example.com")
	defer func() {
		_ = cmd.Flags().Set("auto-tls", "false")
		_ = cmd.Flags().Set("tls-hostname", "")
	}()

	assert.NotPanics(t, func() {
		cmd.PreRun(cmd, []string{})
	})
}

func TestConfigureCmdRun(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"uuid":     "test-uuid-1234",
			"token_id": "test-id-1234",
			"token":    "test-token-1234",
			"api": map[string]interface{}{
				"host": "0.0.0.0",
				"port": 8080,
			},
			"system": map[string]interface{}{
				"data": "/var/lib/pterodactyl/volumes",
				"sftp": map[string]interface{}{
					"bind_port": 2022,
				},
			},
		})
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	cfgTarget := filepath.Join(tmpDir, "config.yml")

	oldArgs := configureArgs
	defer func() { configureArgs = oldArgs }()

	configureArgs.PanelURL = ts.URL
	configureArgs.Token = "ptla_valid_token_12345"
	configureArgs.Node = "1"
	configureArgs.Override = true
	configureArgs.ConfigPath = cfgTarget
	configureArgs.AllowInsecure = true

	assert.NotPanics(t, func() {
		configureCmdRun(nil, nil)
	})
	assert.FileExists(t, cfgTarget)

	// writePanelConfiguration with existing config
	err := writePanelConfiguration(cfgTarget, ts.URL, []byte(`{"token_id":"new-id","token":"new-token"}`))
	assert.NoError(t, err)

	// writePanelConfiguration with invalid json
	err = writePanelConfiguration(cfgTarget, ts.URL, []byte(`{broken`))
	assert.Error(t, err)
}

func TestInitLogging(t *testing.T) {
	tmpDir := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test",
		System: config.SystemConfiguration{
			LogDirectory: filepath.Join(tmpDir, "logs"),
		},
	})
	assert.NotPanics(t, func() {
		initLogging()
	})
	assert.FileExists(t, filepath.Join(tmpDir, "logs", "wings.log"))
}

func TestConfigureQuestionsValidation(t *testing.T) {
	oldArgs := configureArgs
	defer func() { configureArgs = oldArgs }()

	// All empty so all 3 questions generated
	configureArgs.PanelURL = ""
	configureArgs.Token = ""
	configureArgs.Node = ""

	qs := configureQuestions()
	require.Len(t, qs, 3)

	// Question 0: PanelURL
	assert.Equal(t, "PanelURL", qs[0].Name)
	assert.NoError(t, qs[0].Validate("http://panel.example.com"))
	assert.Error(t, qs[0].Validate("::not-a-valid-url::"))
	assert.NoError(t, qs[0].Validate(12345))

	// Question 1: Token
	assert.Equal(t, "Token", qs[1].Name)
	assert.NoError(t, qs[1].Validate("valid-token"))
	assert.Error(t, qs[1].Validate(""))
	assert.NoError(t, qs[1].Validate(12345))

	// Question 2: Node
	assert.Equal(t, "Node", qs[2].Name)
	assert.NoError(t, qs[2].Validate("123"))
	assert.Error(t, qs[2].Validate("not-a-number"))
	assert.NoError(t, qs[2].Validate(12345))
}

func TestBuildDiagnosticsReportComprehensive(t *testing.T) {
	prevArgs := diagnosticsArgs
	defer func() { diagnosticsArgs = prevArgs }()

	// Test case 1: With dockerErr != nil, includeLogs = false, includeEndpoints = false
	diagnosticsArgs.IncludeLogs = false
	diagnosticsArgs.IncludeEndpoints = false
	var out strings.Builder
	buildDiagnosticsReport(&out, types.Version{}, dockersystem.Info{}, errors.New("cannot connect to docker daemon"))
	assert.Contains(t, out.String(), "cannot connect to docker daemon")
	assert.Contains(t, out.String(), "Logs redacted.")

	// Test case 2: dockerErr == nil, full info, includeLogs = true
	diagnosticsArgs.IncludeLogs = true
	diagnosticsArgs.IncludeEndpoints = true
	diagnosticsArgs.LogLines = 10

	v := types.Version{Version: "24.0.5"}
	info := dockersystem.Info{
		ServerVersion: "24.0.5",
		Driver:        "overlay2",
		DriverStatus:  [][2]string{{"Backing Filesystem", "extfs"}},
		SystemStatus:  [][2]string{{"Plugins", "overlay2"}},
		LoggingDriver: "json-file",
		CgroupDriver:  "systemd",
		Warnings:      []string{"warning 1", "warning 2"},
	}

	var out2 strings.Builder
	buildDiagnosticsReport(&out2, v, info, nil)
	assert.Contains(t, out2.String(), "24.0.5")
	assert.Contains(t, out2.String(), "overlay2")
	assert.Contains(t, out2.String(), "Backing Filesystem: extfs")
	assert.Contains(t, out2.String(), "warning 1")
}

func TestConfigureCmdRunTargetAndStatPanic(t *testing.T) {
	cmd := rootCommand
	_ = cmd.Flags().Set("config", "/some/custom/path/config.yml")
	defer func() {
		_ = cmd.Flags().Set("config", "/etc/pterodactyl/config.yml")
	}()

	// Target points to invalid path where parent is a regular file
	tmpFile := filepath.Join(t.TempDir(), "a_file")
	require.NoError(t, os.WriteFile(tmpFile, []byte("x"), 0644))
	badTarget := filepath.Join(tmpFile, "cannot_be_dir", "config.yml")

	oldArgs := configureArgs
	defer func() { configureArgs = oldArgs }()
	configureArgs.ConfigPath = badTarget
	configureArgs.Override = false

	assert.Panics(t, func() {
		configureCmdRun(nil, nil)
	})
}

func TestInitConfigRelativeAndDebug(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "config.yml")
	content := []byte(`
token_id: "test-id"
token: "test-token"
system:
  root_directory: "` + tmpDir + `"
`)
	require.NoError(t, os.WriteFile(cfgFile, content, 0600))

	oldConfigPath := configPath
	oldDebug := debug
	defer func() {
		configPath = oldConfigPath
		debug = oldDebug
	}()

	cwd, err := os.Getwd()
	require.NoError(t, err)
	relPath, err := filepath.Rel(cwd, cfgFile)
	require.NoError(t, err)

	configPath = relPath
	debug = true

	assert.NotPanics(t, func() {
		initConfig()
	})
	assert.True(t, filepath.IsAbs(configPath))
	assert.True(t, config.Get().Debug)
}

func TestShutdownOnSignalManagerError(t *testing.T) {
	signals := make(chan os.Signal, 1)
	signals <- syscall.SIGTERM

	// Manager with invalid root dir so PersistStates fails
	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	tmpFile := filepath.Join(t.TempDir(), "regular_file")
	require.NoError(t, os.WriteFile(tmpFile, []byte("x"), 0644))
	cfg.System.RootDirectory = filepath.Join(tmpFile, "not_dir")
	config.Set(cfg)

	srv := &http.Server{}
	shutdownOnSignal(signals, server.NewEmptyManager(nil), srv)
}







