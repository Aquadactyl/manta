package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
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
