package environment

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"

	"github.com/pterodactyl/wings/config"
)

func resetDockerClientForTest(t *testing.T) {
	t.Helper()
	if _client != nil {
		_ = _client.Close()
	}
	_client = nil
	_conce = sync.Once{}
	t.Cleanup(func() {
		if _client != nil {
			_ = _client.Close()
		}
		_client = nil
		_conce = sync.Once{}
	})
}

func TestCheckDataMountLogsOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		createStatus int
		waitBody     string
		writeMarker  bool
	}{
		{name: "shared", waitBody: `{"StatusCode":0}`, writeMarker: true},
		{name: "mount refused", createStatus: http.StatusInternalServerError},
		{name: "not writable", waitBody: `{"StatusCode":4}`},
		{name: "not shared", waitBody: `{"StatusCode":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetDockerClientForTest(t)
			dir := t.TempDir()
			t.Setenv("DOCKER_HOST", "")
			markerWritten := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/v1.51")
				switch {
				case path == "/_ping":
					w.Header().Set("API-Version", "1.51")
					_, _ = io.WriteString(w, "OK")
				case r.Method == http.MethodGet && path == "/images/ghcr.io/pterodactyl/installers:alpine/json":
					_, _ = io.WriteString(w, `{}`)
				case r.Method == http.MethodPost && path == "/containers/create":
					if tc.createStatus != 0 {
						http.Error(w, `{"message":"refused"}`, tc.createStatus)
						return
					}
					var request struct {
						Env        []string              `json:"Env"`
						HostConfig *container.HostConfig `json:"HostConfig"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Errorf("decode create request: %v", err)
						http.Error(w, "bad request", http.StatusBadRequest)
						return
					}
					if tc.writeMarker && len(request.HostConfig.Mounts) > 0 {
						var marker, token string
						for _, env := range request.Env {
							if strings.HasPrefix(env, "MANTA_FILE=") {
								marker = strings.TrimPrefix(env, "MANTA_FILE=")
							}
							if strings.HasPrefix(env, "MANTA_TOKEN=") {
								token = strings.TrimPrefix(env, "MANTA_TOKEN=")
							}
						}
						if err := os.WriteFile(request.HostConfig.Mounts[0].Source+"/"+marker, []byte(token), 0o600); err != nil {
							t.Errorf("write marker: %v", err)
						} else {
							markerWritten = true
						}
					}
					_, _ = io.WriteString(w, `{"Id":"mount-check","Warnings":[]}`)
				case r.Method == http.MethodPost && path == "/containers/mount-check/start":
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodPost && path == "/containers/mount-check/wait":
					_, _ = io.WriteString(w, tc.waitBody)
				case r.Method == http.MethodDelete && path == "/containers/mount-check":
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected Docker request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			t.Setenv("DOCKER_HOST", srv.URL)
			config.Set(&config.Configuration{System: config.SystemConfiguration{Data: dir}})
			CheckDataMount(context.Background())
			if tc.writeMarker && !markerWritten {
				t.Fatal("Docker create request did not include the marker mount")
			}
		})
	}

	t.Run("skips missing data directory", func(t *testing.T) {
		resetDockerClientForTest(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/_ping" {
				w.Header().Set("API-Version", "1.51")
				_, _ = io.WriteString(w, "OK")
				return
			}
			t.Errorf("unexpected request for missing directory: %s", r.URL)
		}))
		defer srv.Close()
		t.Setenv("DOCKER_HOST", srv.URL)
		config.Set(&config.Configuration{System: config.SystemConfiguration{Data: t.TempDir() + "/missing"}})
		CheckDataMount(context.Background())
	})

	t.Run("skips when Docker client cannot be created", func(t *testing.T) {
		resetDockerClientForTest(t)
		t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
		t.Setenv("DOCKER_CERT_PATH", t.TempDir()+"/missing-certs")
		config.Set(&config.Configuration{})
		CheckDataMount(context.Background())
	})
}
