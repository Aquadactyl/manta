package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
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

