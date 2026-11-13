package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/docker/docker/api/types"
	dockersystem "github.com/docker/docker/api/types/system"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

func setupDiagnosticsRunTest(t *testing.T) {
	t.Helper()
	oldArgs := diagnosticsArgs
	oldAsk, oldAskUpload := askDiagnosticsQuestions, askDiagnosticsUpload
	oldDocker, oldUpload, oldDockerClient := collectDockerInfo, uploadDiagnostics, newDiagnosticsDockerClient
	oldConfigPath := configPath
	t.Cleanup(func() {
		diagnosticsArgs = oldArgs
		askDiagnosticsQuestions, askDiagnosticsUpload = oldAsk, oldAskUpload
		collectDockerInfo, uploadDiagnostics = oldDocker, oldUpload
		newDiagnosticsDockerClient = oldDockerClient
		configPath = oldConfigPath
	})
	diagnosticsArgs = struct {
		IncludeEndpoints   bool
		IncludeLogs        bool
		ReviewBeforeUpload bool
		HastebinURL        string
		LogLines           int
	}{HastebinURL: "https://haste.example", LogLines: DefaultLogLines}
	configPath = t.TempDir() + "/missing-config.yml"
}

func TestNewDiagnosticsCommandPreRun(t *testing.T) {
	oldPath := configPath
	t.Cleanup(func() { configPath = oldPath })
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg, err := config.NewAtPath(path)
	require.NoError(t, err)
	require.NoError(t, config.WriteToDisk(cfg))
	configPath = path

	command := newDiagnosticsCommand()
	command.PreRun(command, nil)
	require.NotNil(t, config.Get())
	require.Equal(t, DefaultHastebinUrl, command.Flags().Lookup("hastebin-url").DefValue)
	require.Equal(t, "200", command.Flags().Lookup("log-lines").DefValue)
}

func TestBuildDiagnosticsReportReportsMissingCommandsAndLogs(t *testing.T) {
	oldPath, oldArgs := configPath, diagnosticsArgs
	t.Cleanup(func() { configPath, diagnosticsArgs = oldPath, oldArgs })
	configPath = filepath.Join(t.TempDir(), "missing.yml")
	diagnosticsArgs.IncludeLogs = true
	diagnosticsArgs.LogLines = 5

	bin := t.TempDir()
	for _, name := range []string{"docker", "tail"} {
		p := filepath.Join(bin, name)
		require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nexit 1\n"), 0o700))
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var output strings.Builder
	buildDiagnosticsReport(&output, types.Version{}, dockersystem.Info{}, errors.New("docker unavailable"))
	require.Contains(t, output.String(), "docker unavailable")
	require.Contains(t, output.String(), "Couldn't list containers")
	require.Contains(t, output.String(), "No logs found or an error occurred.")
}

func TestUploadToHastebinReportsBodyReadFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"key":"short"}`))
	}))
	defer srv.Close()
	_, err := uploadToHastebin(srv.URL, "report")
	require.Error(t, err)
}

func TestGetDockerInfoAPIOutcomes(t *testing.T) {
	old := newDiagnosticsDockerClient
	t.Cleanup(func() { newDiagnosticsDockerClient = old })

	t.Run("client creation failure", func(t *testing.T) {
		newDiagnosticsDockerClient = func() (*client.Client, error) { return nil, errors.New("client unavailable") }
		_, _, err := getDockerInfo()
		require.ErrorContains(t, err, "client unavailable")
	})

	for _, tc := range []struct {
		name string
		fail string
	}{
		{name: "version failure", fail: "version"},
		{name: "info failure", fail: "info"},
		{name: "success"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1.51/version":
					if tc.fail == "version" {
						http.Error(w, `{"message":"version failed"}`, http.StatusInternalServerError)
						return
					}
					_, _ = fmt.Fprint(w, `{"Version":"test"}`)
				case "/v1.51/info":
					if tc.fail == "info" {
						http.Error(w, `{"message":"info failed"}`, http.StatusInternalServerError)
						return
					}
					_, _ = fmt.Fprint(w, `{"Driver":"overlay2"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			dockerClient, err := client.NewClientWithOpts(client.WithHost(srv.URL), client.WithVersion("1.51"))
			require.NoError(t, err)
			newDiagnosticsDockerClient = func() (*client.Client, error) { return dockerClient, nil }
			version, info, err := getDockerInfo()
			if tc.fail == "" {
				require.NoError(t, err)
				require.Equal(t, "test", version.Version)
				require.Equal(t, "overlay2", info.Driver)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestDiagnosticsCommandRunPromptOutcomes(t *testing.T) {
	t.Run("interrupt returns without upload prompt", func(t *testing.T) {
		setupDiagnosticsRunTest(t)
		askDiagnosticsQuestions = func([]*survey.Question, interface{}, ...survey.AskOpt) error { return terminal.InterruptErr }
		called := false
		askDiagnosticsUpload = func(survey.Prompt, interface{}, ...survey.AskOpt) error { called = true; return nil }
		diagnosticsCmdRun(nil, nil)
		require.False(t, called)
	})

	t.Run("unexpected prompt error panics", func(t *testing.T) {
		setupDiagnosticsRunTest(t)
		askDiagnosticsQuestions = func([]*survey.Question, interface{}, ...survey.AskOpt) error { return errors.New("prompt failure") }
		require.Panics(t, func() { diagnosticsCmdRun(nil, nil) })
	})

	t.Run("continues after successful questions and ignores upload prompt error", func(t *testing.T) {
		setupDiagnosticsRunTest(t)
		askDiagnosticsQuestions = func(q []*survey.Question, _ interface{}, _ ...survey.AskOpt) error {
			require.Len(t, q, 3)
			return nil
		}
		collectDockerInfo = func() (types.Version, dockersystem.Info, error) {
			return types.Version{}, dockersystem.Info{}, errors.New("docker offline")
		}
		askDiagnosticsUpload = func(_ survey.Prompt, response interface{}, _ ...survey.AskOpt) error {
			*(response.(*bool)) = false
			return errors.New("upload prompt interrupted")
		}
		diagnosticsCmdRun(nil, nil)
	})
}

func TestDiagnosticsCommandRunReviewsAndUploads(t *testing.T) {
	for _, tc := range []struct {
		name      string
		uploadErr error
	}{
		{name: "success"},
		{name: "failed upload", uploadErr: errors.New("haste unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupDiagnosticsRunTest(t)
			diagnosticsArgs.ReviewBeforeUpload = true
			askDiagnosticsQuestions = func([]*survey.Question, interface{}, ...survey.AskOpt) error { return nil }
			collectDockerInfo = func() (types.Version, dockersystem.Info, error) {
				return types.Version{Version: "test"}, dockersystem.Info{ServerVersion: "test"}, nil
			}
			askDiagnosticsUpload = func(_ survey.Prompt, response interface{}, _ ...survey.AskOpt) error {
				*(response.(*bool)) = true
				return nil
			}
			called := false
			uploadDiagnostics = func(_ string, report string) (string, error) {
				called = true
				require.Contains(t, report, "Diagnostics Report")
				return "https://haste.example/abc", tc.uploadErr
			}
			diagnosticsCmdRun(nil, nil)
			require.True(t, called)
		})
	}
}
