package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types"
	dockersystem "github.com/docker/docker/api/types/system"
	"github.com/stretchr/testify/require"
)

func withDockerServer(t *testing.T, statusPath string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch path {
		case "/_ping":
			w.Header().Set("API-Version", "1.51")
			_, _ = fmt.Fprint(w, "OK")
		case "/version", "/v1.51/version":
			if statusPath == "version" {
				http.Error(w, `{"message":"version unavailable"}`, status)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"Version":"test-docker"}`)
		case "/info", "/v1.51/info":
			if statusPath == "info" {
				http.Error(w, `{"message":"info unavailable"}`, status)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"Driver":"overlay2","CgroupDriver":"systemd","CgroupVersion":"2","Containers":4,"ContainersRunning":2,"ContainersPaused":1,"ContainersStopped":1,"MemTotal":4096,"RuncCommit":{"ID":"runc-test"},"DriverStatus":[["Backing Filesystem","ext4"],["Other","value"]]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv("DOCKER_HOST", srv.URL)
	t.Setenv("DOCKER_CERT_PATH", "")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Cleanup(srv.Close)
	return srv
}

func TestGetDockerInfoAPI(t *testing.T) {
	t.Run("returns version and daemon information", func(t *testing.T) {
		withDockerServer(t, "", 0)
		version, info, err := GetDockerInfo(context.Background())
		require.NoError(t, err)
		require.Equal(t, "test-docker", version.Version)
		require.Equal(t, "overlay2", info.Driver)
		require.Len(t, info.DriverStatus, 2)
	})

	for _, path := range []string{"version", "info"} {
		t.Run(path+" endpoint failure", func(t *testing.T) {
			withDockerServer(t, path, http.StatusInternalServerError)
			_, _, err := GetDockerInfo(context.Background())
			require.Error(t, err)
		})
	}
}

func TestGetSystemInformationAPI(t *testing.T) {
	withDockerServer(t, "", 0)
	info, err := GetSystemInformation()
	require.NoError(t, err)
	require.Equal(t, Version, info.Version)
	require.Equal(t, "test-docker", info.Docker.Version)
	require.Equal(t, "overlay2", info.Docker.Storage.Driver)
	require.Equal(t, "ext4", info.Docker.Storage.Filesystem)
	require.Equal(t, "runc-test", info.Docker.Runc.Version)
	require.Equal(t, 4, info.Docker.Containers.Total)
	require.Equal(t, 2, info.Docker.Containers.Running)
	require.Equal(t, 1, info.Docker.Containers.Paused)
	require.Equal(t, 1, info.Docker.Containers.Stopped)
	require.True(t, info.Capabilities["system_usage"])
	_, err = json.Marshal(info)
	require.NoError(t, err)
}

func TestGetSystemInformationPropagatesHostAndDockerErrors(t *testing.T) {
	oldKernel, oldDocker, oldOS := systemHostKernelVersion, systemGetDockerInfo, systemHostOSName
	t.Cleanup(func() { systemHostKernelVersion, systemGetDockerInfo, systemHostOSName = oldKernel, oldDocker, oldOS })

	t.Run("kernel error", func(t *testing.T) {
		want := errors.New("kernel unavailable")
		systemHostKernelVersion = func() (string, error) { return "", want }
		_, err := GetSystemInformation()
		require.ErrorIs(t, err, want)
	})
	t.Run("docker error", func(t *testing.T) {
		systemHostKernelVersion = func() (string, error) { return "kernel", nil }
		want := errors.New("docker unavailable")
		systemGetDockerInfo = func(context.Context) (types.Version, dockersystem.Info, error) {
			return types.Version{}, dockersystem.Info{}, want
		}
		_, err := GetSystemInformation()
		require.ErrorIs(t, err, want)
	})
	t.Run("os error", func(t *testing.T) {
		systemHostKernelVersion = func() (string, error) { return "kernel", nil }
		systemGetDockerInfo = func(context.Context) (types.Version, dockersystem.Info, error) {
			return types.Version{}, dockersystem.Info{}, nil
		}
		want := errors.New("os unavailable")
		systemHostOSName = func(dockersystem.Info) (string, error) { return "", want }
		_, err := GetSystemInformation()
		require.ErrorIs(t, err, want)
	})
}

func TestGetDockerInfoRejectsInvalidTLSConfiguration(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	t.Setenv("DOCKER_CERT_PATH", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("DOCKER_TLS_VERIFY", "")
	_, _, err := GetDockerInfo(context.Background())
	require.Error(t, err)
}
