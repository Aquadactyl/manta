package docker

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/events"
)

func TestCreateReturnsContainerInspectError(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	previousFastEnabled := fastEnabled
	fastEnabled = false
	t.Cleanup(func() { fastEnabled = previousFastEnabled })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.51/containers/container-id/json" {
			t.Errorf("unexpected Docker API request: %s %s", r.Method, r.URL.Path)
		}
		http.Error(w, `{"message":"daemon unavailable"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()

	e := &Environment{
		Id:            "container-id",
		Configuration: environment.NewConfiguration(environment.Settings{}, nil),
		meta:          &Metadata{Image: "example.test/image:latest"},
		client:        cli,
		emitter:       events.NewBus(),
	}
	err = e.Create()
	require.ErrorContains(t, err, "failed to inspect container")
}

func TestCreateReturnsImageAvailabilityError(t *testing.T) {
	cfg := &config.Configuration{AuthenticationToken: "test-token"}
	cfg.Docker.ImagePullPolicy = config.ImagePullPolicyNever
	config.Set(cfg)
	previousFastEnabled := fastEnabled
	fastEnabled = false
	t.Cleanup(func() { fastEnabled = previousFastEnabled })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.51/containers/container-id/json":
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		case "/v1.51/images/example.test/image:latest/json":
			http.Error(w, `{"message":"registry unavailable"}`, http.StatusInternalServerError)
		default:
			t.Errorf("unexpected Docker API request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()

	e := &Environment{
		Id:            "container-id",
		Configuration: environment.NewConfiguration(environment.Settings{}, nil),
		meta:          &Metadata{Image: "example.test/image:latest"},
		client:        cli,
		emitter:       events.NewBus(),
	}
	err = e.Create()
	require.Error(t, err)
}

func TestCreateExistingContainerDoesNotCallDockerAgain(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	previousFastEnabled := fastEnabled
	fastEnabled = false
	t.Cleanup(func() { fastEnabled = previousFastEnabled })

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/v1.51/containers/container-id/json" {
			t.Errorf("unexpected Docker API request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"Id":"container-id"}`))
	}))
	defer server.Close()

	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()

	e := &Environment{Id: "container-id", meta: &Metadata{}, client: cli}
	require.NoError(t, e.Create())
	require.True(t, called)
}

func TestCreateRejectsInvalidSELinuxMode(t *testing.T) {
	cfg := &config.Configuration{AuthenticationToken: "test-token"}
	cfg.Docker.SELinuxRelabel = "invalid"
	config.Set(cfg)
	previousFastEnabled := fastEnabled
	fastEnabled = false
	t.Cleanup(func() { fastEnabled = previousFastEnabled })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.51/containers/container-id/json" {
			t.Errorf("unexpected Docker API request: %s %s", r.Method, r.URL.Path)
		}
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()

	e := &Environment{
		Id:            "container-id",
		Configuration: environment.NewConfiguration(environment.Settings{Labels: map[string]string{"custom": "label"}}, nil),
		meta:          &Metadata{Image: "~local-image"},
		client:        cli,
		emitter:       events.NewBus(),
	}
	require.ErrorContains(t, e.Create(), "invalid SELinux relabel mode")
}

func TestCreateReturnsDockerContainerCreateError(t *testing.T) {
	cfg := &config.Configuration{AuthenticationToken: "test-token"}
	cfg.Docker.SELinuxRelabel = "shared"
	config.Set(cfg)
	previousFastEnabled := fastEnabled
	fastEnabled = false
	t.Cleanup(func() { fastEnabled = previousFastEnabled })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.51/containers/container-id/json":
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		case "/v1.51/containers/create":
			http.Error(w, `{"message":"create failed"}`, http.StatusInternalServerError)
		default:
			t.Errorf("unexpected Docker API request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()

	e := &Environment{
		Id:            "container-id",
		Configuration: environment.NewConfiguration(environment.Settings{}, nil),
		meta:          &Metadata{Image: "~local-image"},
		client:        cli,
		emitter:       events.NewBus(),
	}
	require.ErrorContains(t, e.Create(), "failed to create container")
}

func TestCreateForceOutgoingIPNetworkErrors(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		networkInspectStatus int
		wantCreate           bool
	}{
		{name: "inspect error", networkInspectStatus: http.StatusInternalServerError},
		{name: "create error", networkInspectStatus: http.StatusNotFound, wantCreate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config.Set(&config.Configuration{AuthenticationToken: "test-token"})
			previousFastEnabled := fastEnabled
			fastEnabled = false
			t.Cleanup(func() { fastEnabled = previousFastEnabled })

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1.51/containers/container-id/json":
					http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				case "/v1.51/networks/ip-10-0-0-1":
					http.Error(w, `{"message":"network error"}`, tc.networkInspectStatus)
				case "/v1.51/networks/create":
					if !tc.wantCreate {
						t.Errorf("unexpected network creation request")
					}
					http.Error(w, `{"message":"network create failed"}`, http.StatusInternalServerError)
				default:
					t.Errorf("unexpected Docker API request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
			require.NoError(t, err)
			defer cli.Close()

			allocations := environment.Allocations{ForceOutgoingIP: true}
			allocations.DefaultMapping.Ip = "10.0.0.1"
			e := &Environment{
				Id:            "container-id",
				Configuration: environment.NewConfiguration(environment.Settings{Allocations: allocations}, nil),
				meta:          &Metadata{Image: "~local-image"},
				client:        cli,
				emitter:       events.NewBus(),
			}
			require.Error(t, e.Create())
		})
	}
}
