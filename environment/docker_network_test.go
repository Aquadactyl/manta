package environment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

func TestConfigureDockerErrorAndOverlayNetwork(t *testing.T) {
	// This test is also run by itself in the Darwin coverage workflow, where no
	// earlier test has initialized the process-wide configuration.
	initialConfig, err := config.NewAtPath("")
	require.NoError(t, err)
	config.Set(initialConfig)
	previousConfig, previousFactory := config.Get(), dockerClientFactory
	t.Cleanup(func() {
		config.Set(previousConfig)
		dockerClientFactory = previousFactory
	})

	want := errors.New("docker client unavailable")
	dockerClientFactory = func() (*client.Client, error) { return nil, want }
	require.ErrorIs(t, ConfigureDocker(context.Background()), want)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1.41/networks/test-network" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"Driver":"overlay"}`))
	}))
	defer api.Close()
	cli, err := client.NewClientWithOpts(client.WithHost(api.URL), client.WithVersion("1.41"))
	require.NoError(t, err)
	defer cli.Close()
	dockerClientFactory = func() (*client.Client, error) { return cli, nil }
	cfg := &config.Configuration{}
	cfg.Docker.Network.Name = "test-network"
	config.Set(cfg)

	require.NoError(t, ConfigureDocker(context.Background()))
	require.Equal(t, "overlay", config.Get().Docker.Network.Driver)
	require.Empty(t, config.Get().Docker.Network.Interface)
	require.True(t, config.Get().Docker.Network.ISPN)
}

func TestCreateDockerNetworkDrivers(t *testing.T) {
	config.Set(&config.Configuration{})
	previous := config.Get()
	t.Cleanup(func() { config.Set(previous) })

	for _, driver := range []string{"bridge", "host", "overlay", "weavemesh"} {
		t.Run(driver, func(t *testing.T) {
			var created map[string]any
			handlerErr := make(chan error, 1)
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1.41/networks/create" {
					err := fmt.Errorf("unexpected Docker API path %q", r.URL.Path)
					handlerErr <- err
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
					handlerErr <- err
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				handlerErr <- nil
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"Id":"network-id","Warning":""}`))
			}))
			t.Cleanup(api.Close)

			cfg := &config.Configuration{}
			cfg.Docker.Network.Name = "test-network"
			cfg.Docker.Network.Driver = driver
			cfg.Docker.Network.EnableICC = true
			cfg.Docker.Network.NetworkMTU = 1400
			cfg.Docker.Network.Interfaces.V4.Subnet = "10.0.0.0/24"
			cfg.Docker.Network.Interfaces.V4.Gateway = "10.0.0.1"
			cfg.Docker.Network.Interfaces.V6.Enabled = true
			cfg.Docker.Network.Interfaces.V6.Subnet = "fd00::/64"
			cfg.Docker.Network.Interfaces.V6.Gateway = "fd00::1"
			config.Set(cfg)

			cli, err := client.NewClientWithOpts(
				client.WithHost(api.URL),
				client.WithVersion("1.41"),
				client.WithHTTPClient(api.Client()),
			)
			require.NoError(t, err)

			require.NoError(t, createDockerNetwork(context.Background(), cli))
			require.NoError(t, <-handlerErr)
			require.Equal(t, driver, created["Driver"])
			if driver == "bridge" {
				require.Equal(t, "10.0.0.1", config.Get().Docker.Network.Interface)
			} else {
				require.Empty(t, config.Get().Docker.Network.Interface)
			}
		})
	}
}

func TestCreateDockerNetworkReturnsAPIError(t *testing.T) {
	config.Set(&config.Configuration{})
	previous := config.Get()
	t.Cleanup(func() { config.Set(previous) })
	config.Set(&config.Configuration{})

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "docker API unavailable", http.StatusInternalServerError)
	}))
	defer api.Close()

	cli, err := client.NewClientWithOpts(
		client.WithHost(api.URL),
		client.WithVersion("1.41"),
		client.WithHTTPClient(api.Client()),
	)
	require.NoError(t, err)
	err = createDockerNetwork(context.Background(), cli)
	require.Error(t, err)
}
