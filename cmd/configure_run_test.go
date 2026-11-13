package cmd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type configureExitPanic int

func setupConfigureRun(t *testing.T, panelURL, target string) {
	t.Helper()
	oldArgs, oldConfigPath := configureArgs, configPath
	oldAsk, oldAskOne, oldExit, oldNewRequest, oldDoRequest := askConfigureQuestions, askConfigureOverride, exitConfigure, newConfigureHTTPRequest, doConfigureRequest
	oldTransport := http.DefaultTransport
	t.Cleanup(func() {
		configureArgs, configPath = oldArgs, oldConfigPath
		askConfigureQuestions, askConfigureOverride, exitConfigure = oldAsk, oldAskOne, oldExit
		newConfigureHTTPRequest = oldNewRequest
		doConfigureRequest = oldDoRequest
		http.DefaultTransport = oldTransport
	})
	configureArgs = struct {
		PanelURL      string
		Token         string
		ConfigPath    string
		Node          string
		Override      bool
		AllowInsecure bool
	}{PanelURL: panelURL, Token: "test-token", ConfigPath: target, Node: "1"}
	configPath = filepath.Join(t.TempDir(), "global.yml")
	askConfigureQuestions = func([]*survey.Question, interface{}, ...survey.AskOpt) error { return nil }
	askConfigureOverride = func(_ survey.Prompt, _ interface{}, _ ...survey.AskOpt) error { return nil }
	exitConfigure = func(code int) { panic(configureExitPanic(code)) }
}

func TestConfigureCommandPromptOutcomes(t *testing.T) {
	t.Run("existing configuration is preserved on declined override", func(t *testing.T) {
		panel := newFakePanel(t, `{}`)
		target := filepath.Join(t.TempDir(), "config.yml")
		require.NoError(t, os.WriteFile(target, []byte("existing"), 0o600))
		setupConfigureRun(t, panel.URL, target)
		prompted := false
		askConfigureOverride = func(_ survey.Prompt, response interface{}, _ ...survey.AskOpt) error {
			prompted = true
			*(response.(*bool)) = false
			return errors.New("prompt interrupted")
		}
		require.PanicsWithValue(t, configureExitPanic(1), func() { configureCmdRun(nil, nil) })
		require.True(t, prompted)
		require.Equal(t, "existing", string(mustReadFile(t, target)))
	})

	t.Run("survey interrupt returns", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "missing.yml")
		setupConfigureRun(t, "http://panel.example", target)
		askConfigureQuestions = func([]*survey.Question, interface{}, ...survey.AskOpt) error { return terminal.InterruptErr }
		require.NotPanics(t, func() { configureCmdRun(nil, nil) })
	})

	t.Run("unexpected survey error panics", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "missing.yml")
		setupConfigureRun(t, "http://panel.example", target)
		askConfigureQuestions = func([]*survey.Question, interface{}, ...survey.AskOpt) error { return errors.New("input failed") }
		require.Panics(t, func() { configureCmdRun(nil, nil) })
	})
}

func TestConfigureCommandHandlesPanelStatuses(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			defer panel.Close()
			setupConfigureRun(t, panel.URL, filepath.Join(t.TempDir(), "config.yml"))
			require.PanicsWithValue(t, configureExitPanic(1), func() { configureCmdRun(nil, nil) })
		})
	}

	t.Run("request failure exits", func(t *testing.T) {
		panel := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := panel.URL
		panel.Close()
		setupConfigureRun(t, url, filepath.Join(t.TempDir(), "config.yml"))
		require.PanicsWithValue(t, configureExitPanic(1), func() { configureCmdRun(nil, nil) })
	})

	t.Run("configuration write failure exits", func(t *testing.T) {
		panel := newFakePanel(t, `{"token_id":"id","token":"token"}`)
		targetDir := t.TempDir()
		target := filepath.Join(targetDir, "directory-config.yml")
		require.NoError(t, os.Mkdir(target, 0o700))
		setupConfigureRun(t, panel.URL, target)
		configureArgs.Override = true
		require.PanicsWithValue(t, configureExitPanic(1), func() { configureCmdRun(nil, nil) })
	})
}

func TestConfigureCommandSuccessWithInsecureOptIn(t *testing.T) {
	panel := newFakePanel(t, `{"uuid":"11111111-2222-3333-4444-555555555555","token_id":"panel-id","token":"panel-token"}`)
	target := filepath.Join(t.TempDir(), "config.yml")
	setupConfigureRun(t, panel.URL, target)
	configureArgs.AllowInsecure = true
	configureCmdRun(nil, nil)
	require.FileExists(t, target)
}

func TestConfigureCommandUsesGlobalConfigPathFlag(t *testing.T) {
	panel := newFakePanel(t, `{"token_id":"panel-id","token":"panel-token"}`)
	globalPath := filepath.Join(t.TempDir(), "global.yml")
	setupConfigureRun(t, panel.URL, filepath.Join(t.TempDir(), "configure.yml"))
	configPath = globalPath
	cmd := &cobra.Command{Use: "configure"}
	cmd.Flags().String("config-path", "", "")
	cmd.Flags().String("config", "", "")
	require.NoError(t, cmd.Flags().Set("config", globalPath))
	configureCmdRun(cmd, nil)
	require.FileExists(t, globalPath)
}

func TestConfigureCommandPanicsWhenRequestCannotBeCreated(t *testing.T) {
	setupConfigureRun(t, "http://panel.example", filepath.Join(t.TempDir(), "config.yml"))
	newConfigureHTTPRequest = func(string, string, io.Reader) (*http.Request, error) { return nil, errors.New("bad request") }
	require.Panics(t, func() { configureCmdRun(nil, nil) })
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("response read failed") }
func (failingReadCloser) Close() error             { return nil }

func TestConfigureCommandPanicsWhenPanelBodyCannotBeRead(t *testing.T) {
	setupConfigureRun(t, "http://panel.example", filepath.Join(t.TempDir(), "config.yml"))
	doConfigureRequest = func(_ *http.Client, _ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: failingReadCloser{}}, nil
	}
	require.Panics(t, func() { configureCmdRun(nil, nil) })
}

func TestGetRequestRejectsMalformedPanelURL(t *testing.T) {
	old := configureArgs
	t.Cleanup(func() { configureArgs = old })
	configureArgs.PanelURL = "http://panel.example/%zz"
	require.Panics(t, func() { _, _ = getRequest() })
}

func TestWritePanelConfigurationRejectsMalformedAllowedMounts(t *testing.T) {
	clearTokenEnvironment(t)
	err := writePanelConfiguration(filepath.Join(t.TempDir(), "config.yml"), "http://panel.test", []byte(`{"token_id":"id","token":"token","allowed_mounts":"not-a-list"}`))
	require.Error(t, err)
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}
