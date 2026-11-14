package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/system"
)

func TestPollResourcesErrorAndStopPaths(t *testing.T) {
	previousCLI, previousFast := cli, fastEnabled
	t.Cleanup(func() {
		o = sync.Once{}
		cli, fastEnabled = previousCLI, previousFast
	})
	config.Set(&config.Configuration{AuthenticationToken: "test-token", Docker: config.DockerConfiguration{UsePerformantInspect: true}})
	fastEnabled = true

	for _, tc := range []struct {
		name            string
		statsStatus     int
		statsBody       string
		startedAt       string
		cancelOnInspect bool
		wantStatsErr    bool
		wantCanceledErr bool
	}{
		{name: "stats request error", statsStatus: http.StatusInternalServerError, wantStatsErr: true},
		{name: "invalid stats frame", statsBody: "{\"memory_stats\":\n"},
		{name: "invalid uptime then EOF", statsBody: "{}\n", startedAt: "not-a-time"},
		{name: "context canceled during uptime", statsBody: "{}\n", startedAt: "2026-11-14T00:00:00Z", cancelOnInspect: true, wantCanceledErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/stats"):
					status := tc.statsStatus
					if status == 0 {
						status = http.StatusOK
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(tc.statsBody))
				case strings.HasSuffix(r.URL.Path, "/json"):
					_, _ = w.Write([]byte(`{"State":{"Running":true,"StartedAt":"` + tc.startedAt + `"}}`))
					if tc.cancelOnInspect {
						cancel()
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer api.Close()

			cli, err := client.NewClientWithOpts(client.WithHost(api.URL), client.WithVersion("1.51"))
			require.NoError(t, err)
			defer cli.Close()
			o = sync.Once{}
			e := &Environment{Id: "poll-resources", client: cli, st: system.NewAtomicString(environment.ProcessRunningState), emitter: events.NewBus()}
			err = e.pollResources(ctx)
			if tc.wantStatsErr {
				require.Error(t, err)
			} else if tc.wantCanceledErr {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPollResourcesStopsAfterServerTurnsOffline(t *testing.T) {
	previousCLI, previousFast := cli, fastEnabled
	t.Cleanup(func() {
		o = sync.Once{}
		cli, fastEnabled = previousCLI, previousFast
	})
	config.Set(&config.Configuration{AuthenticationToken: "test-token", Docker: config.DockerConfiguration{UsePerformantInspect: true}})
	fastEnabled = true
	started, release := make(chan struct{}), make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stats"):
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			close(started)
			<-release
			_, _ = w.Write([]byte("{}\n"))
		case strings.HasSuffix(r.URL.Path, "/json"):
			_, _ = w.Write([]byte(`{"State":{"Running":true,"StartedAt":"2026-11-14T00:00:00Z"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	cli, err := client.NewClientWithOpts(client.WithHost(api.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()
	o = sync.Once{}
	e := &Environment{Id: "poll-resources-offline", client: cli, st: system.NewAtomicString(environment.ProcessRunningState), emitter: events.NewBus()}
	done := make(chan error, 1)
	go func() { done <- e.pollResources(context.Background()) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stats stream")
	}
	e.SetState(environment.ProcessOfflineState)
	close(release)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("poller did not stop after the process went offline")
	}
}
