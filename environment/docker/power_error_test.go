package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/system"
)

func TestStartupReturnsDockerRemovalAndInspectionErrors(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"injected docker error"}`))
	}))
	defer api.Close()

	cli, err := client.NewClientWithOpts(client.WithHost(api.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()
	e := &Environment{
		Id:      "container-id",
		client:  cli,
		st:      system.NewAtomicString(environment.ProcessOfflineState),
		emitter: events.NewBus(),
	}

	require.ErrorContains(t, e.OnBeforeStart(context.Background()), "failed to remove container during pre-boot")
	require.ErrorContains(t, e.Start(context.Background()), "failed to inspect container")
}

func TestStartReturnsAttachError(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.51/containers/container-id/json":
			_, _ = w.Write([]byte(`{"State":{"Running":false}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1.51/containers/container-id":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v1.51/containers/container-id/attach":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"attach failed"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	cli, err := client.NewClientWithOpts(client.WithHost(api.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()
	e := &Environment{
		Id:            "container-id",
		Configuration: environment.NewConfiguration(environment.Settings{}, nil),
		meta:          &Metadata{Image: "alpine:latest"},
		client:        cli,
		st:            system.NewAtomicString(environment.ProcessOfflineState),
		emitter:       events.NewBus(),
	}

	require.ErrorContains(t, e.Start(context.Background()), "failed to attach to container")
}

func TestStartReturnsLogTruncateError(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	logDirectory := t.TempDir()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1.51/containers/container-id/json" {
			_, _ = w.Write([]byte(`{"State":{"Running":false},"LogPath":` + `"` + filepath.ToSlash(logDirectory) + `"` + `}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer api.Close()

	cli, err := client.NewClientWithOpts(client.WithHost(api.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()
	e := &Environment{Id: "container-id", client: cli}

	require.ErrorContains(t, e.Start(context.Background()), "failed to truncate instance logs")
}

func TestStartReturnsContainerStartError(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.51/containers/container-id/json":
			_, _ = w.Write([]byte(`{"State":{"Running":false}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1.51/containers/container-id":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v1.51/containers/container-id/start":
			http.Error(w, `{"message":"start failed"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	cli, err := client.NewClientWithOpts(client.WithHost(api.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()
	e := &Environment{
		Id:            "container-id",
		Configuration: environment.NewConfiguration(environment.Settings{}, nil),
		meta:          &Metadata{Image: "alpine:latest"},
		client:        cli,
		st:            system.NewAtomicString(environment.ProcessOfflineState),
		emitter:       events.NewBus(),
	}
	oldAttach := dockerAttachForStart
	dockerAttachForStart = func(*Environment, context.Context) error { return nil }
	t.Cleanup(func() { dockerAttachForStart = oldAttach })

	require.ErrorContains(t, e.Start(context.Background()), "failed to start container")
	require.Equal(t, environment.ProcessOfflineState, e.State())
}

func TestSignalContainerReturnsDockerKillError(t *testing.T) {
	previousClient, previousEnabled := cli, fastEnabled
	t.Cleanup(func() { o = sync.Once{}; cli, fastEnabled = previousClient, previousEnabled })
	config.Set(&config.Configuration{AuthenticationToken: "test-token", Docker: config.DockerConfiguration{UsePerformantInspect: true}})
	o = sync.Once{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/containers/container-id/json":
			_, _ = w.Write([]byte(`{"State":{"Running":true}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1.51/containers/container-id/kill":
			http.Error(w, `{"message":"kill failed"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	cli, err := client.NewClientWithOpts(client.WithHost(api.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()
	e := &Environment{Id: "container-id", client: cli, st: system.NewAtomicString(environment.ProcessRunningState), emitter: events.NewBus()}
	require.ErrorContains(t, e.SignalContainer(context.Background(), "SIGTERM"), "kill failed")
}

func TestReattachStopsWhenContainerStopsDuringFailedAttach(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	previousFastEnabled := fastEnabled
	fastEnabled = false
	t.Cleanup(func() { fastEnabled = previousFastEnabled })
	inspectCount := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.51/containers/container-id/json":
			inspectCount++
			if inspectCount == 1 {
				_, _ = w.Write([]byte(`{"State":{"Running":true,"Pid":1}}`))
			} else {
				_, _ = w.Write([]byte(`{"State":{"Running":false}}`))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v1.51/containers/container-id/attach":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"attach failed"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	cli, err := client.NewClientWithOpts(client.WithHost("tcp://"+api.Listener.Addr().String()), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()
	e := &Environment{
		Id:            "container-id",
		Configuration: environment.NewConfiguration(environment.Settings{}, nil),
		client:        cli,
		st:            system.NewAtomicString(environment.ProcessRunningState),
		emitter:       events.NewBus(),
	}

	require.False(t, e.reattach())
	require.Equal(t, 2, inspectCount)
	require.Equal(t, 1, e.reattachedCount)
}

func TestReattachTerminatesWhenContainerRemainsUnmanageable(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	previousFastEnabled := fastEnabled
	fastEnabled = false
	t.Cleanup(func() { fastEnabled = previousFastEnabled })
	inspectCount := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.51/containers/container-id/json":
			inspectCount++
			if inspectCount == 1 {
				_, _ = w.Write([]byte(`{"State":{"Running":true,"Pid":1}}`))
				return
			}
			http.Error(w, `{"message":"inspect failed"}`, http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/v1.51/containers/container-id/attach":
			http.Error(w, `{"message":"attach failed"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	cli, err := client.NewClientWithOpts(client.WithHost("tcp://"+api.Listener.Addr().String()), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()
	e := &Environment{
		Id:            "container-id",
		Configuration: environment.NewConfiguration(environment.Settings{}, nil),
		client:        cli,
		st:            system.NewAtomicString(environment.ProcessRunningState),
		emitter:       events.NewBus(),
	}

	require.True(t, e.reattach())
	require.Equal(t, 3, inspectCount)
	require.Equal(t, 1, e.reattachedCount)
}

func TestInSituUpdateReturnsContainerUpdateError(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1.51/containers/container-id/json" {
			_, _ = w.Write([]byte(`{"State":null}`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1.51/containers/container-id/update" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"update failed"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer api.Close()

	cli, err := client.NewClientWithOpts(client.WithHost(api.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()
	e := &Environment{
		Id:            "container-id",
		Configuration: environment.NewConfiguration(environment.Settings{}, nil),
		client:        cli,
		emitter:       events.NewBus(),
	}

	require.ErrorContains(t, e.InSituUpdate(), "could not update container")
}
