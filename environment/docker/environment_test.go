package docker

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
)

func TestDockerEnvironmentMethods(t *testing.T) {
	config.Set(&config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	})
	cfg := environment.NewConfiguration(environment.Settings{
		Mounts: []environment.Mount{
			{Target: "/data", Source: "/host/data", ReadOnly: true},
		},
	}, []string{})

	meta := &Metadata{Image: "alpine:latest"}
	env, err := New("test-server-uuid", meta, cfg)
	require.NoError(t, err)
	assert.Equal(t, "docker", env.Type())
	assert.Equal(t, cfg, env.Config())
	assert.NotNil(t, env.Events())

	// SetImage
	env.SetImage("debian:latest")
	assert.Equal(t, "debian:latest", env.meta.Image)

	// SetStopConfiguration
	env.SetStopConfiguration(remote.ProcessStopConfiguration{Type: "stop"})
	assert.Equal(t, "stop", env.meta.Stop.Type)

	// Stream and IsAttached
	assert.False(t, env.IsAttached())
	env.SetStream(&types.HijackedResponse{})
	assert.True(t, env.IsAttached())
	env.SetStream(nil)
	assert.False(t, env.IsAttached())

	// State changes
	assert.Equal(t, environment.ProcessOfflineState, env.State())

	ch := make(chan []byte, 1)
	env.Events().On(ch)

	env.SetState(environment.ProcessStartingState)
	assert.Equal(t, environment.ProcessStartingState, env.State())

	env.SetState(environment.ProcessRunningState)
	assert.Equal(t, environment.ProcessRunningState, env.State())

	env.SetState(environment.ProcessStoppingState)
	assert.Equal(t, environment.ProcessStoppingState, env.State())

	env.SetState(environment.ProcessOfflineState)
	assert.Equal(t, environment.ProcessOfflineState, env.State())

	assert.Panics(t, func() {
		env.SetState("invalid-state")
	})

	// SetLogCallback
	env.SetLogCallback(func(line []byte) {})
	assert.NotNil(t, env.logCallback)

	// convertMounts
	mounts := env.convertMounts()
	require.Len(t, mounts, 1)
	assert.Equal(t, "/data", mounts[0].Target)
	assert.Equal(t, "/host/data", mounts[0].Source)
	assert.True(t, mounts[0].ReadOnly)

	// Exists & ExitState on nonexistent container
	exists, err := env.Exists()
	assert.NoError(t, err)
	assert.False(t, exists)

	code, oom, err := env.ExitState()
	assert.NoError(t, err)
	assert.Equal(t, uint32(1), code)
	assert.False(t, oom)

	// Uptime on nonexistent container
	uptime, err := env.Uptime(context.Background())
	assert.Error(t, err)
	assert.Equal(t, int64(0), uptime)
}
