package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

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





