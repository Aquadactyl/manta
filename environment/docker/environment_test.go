package docker

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/apex/log"
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

func TestDockerContainerCreateDestroy(t *testing.T) {
	dataDir := t.TempDir()
	cfg := &config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	cfg.Docker.Network.Mode = "bridge"
	cfg.Docker.TmpfsSize = 100
	cfg.System.User.Uid = 0
	cfg.System.User.Gid = 0
	config.Set(cfg)

	envCfg := environment.NewConfiguration(environment.Settings{
		Mounts: []environment.Mount{
			{Target: "/home/container", Source: dataDir, ReadOnly: false},
		},
	}, []string{})

	meta := &Metadata{
		Image: "ghcr.io/pterodactyl/installers:alpine",
		Stop:  remote.ProcessStopConfiguration{Type: "stop"},
	}

	containerID := "test-manta-container-1"
	env, err := New(containerID, meta, envCfg)
	require.NoError(t, err)

	// Ensure cleanup
	defer func() {
		_ = env.Destroy()
	}()

	// Exists initially false
	exists, err := env.Exists()
	require.NoError(t, err)
	assert.False(t, exists)

	// Create
	err = env.Create()
	require.NoError(t, err)

	// Exists should now be true
	exists, err = env.Exists()
	require.NoError(t, err)
	assert.True(t, exists)

	// Calling Create again should be idempotent
	err = env.Create()
	require.NoError(t, err)

	// InSituUpdate
	err = env.InSituUpdate()
	require.NoError(t, err)

	// IsRunning should be false
	running, err := env.IsRunning(context.Background())
	require.NoError(t, err)
	assert.False(t, running)

	// Destroy
	err = env.Destroy()
	require.NoError(t, err)

	// Destroy again should be idempotent
	err = env.Destroy()
	require.NoError(t, err)

	exists, err = env.Exists()
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestDockerContainerStartAndControl(t *testing.T) {
	dataDir := t.TempDir()
	cfg := &config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	cfg.Docker.Network.Mode = "bridge"
	cfg.Docker.TmpfsSize = 100
	cfg.System.User.Uid = 0
	cfg.System.User.Gid = 0
	config.Set(cfg)

	envCfg := environment.NewConfiguration(environment.Settings{
		Mounts: []environment.Mount{
			{Target: "/home/container", Source: dataDir, ReadOnly: false},
		},
	}, []string{})

	meta := &Metadata{
		Image: "ghcr.io/pterodactyl/installers:alpine",
		Stop:  remote.ProcessStopConfiguration{Type: "signal", Value: "SIGTERM"},
	}

	containerID := "test-manta-container-running"
	env, err := New(containerID, meta, envCfg)
	require.NoError(t, err)

	env.SetLogCallback(func(line []byte) {})

	defer func() {
		_ = env.Destroy()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Start container
	err = env.Start(ctx)
	require.NoError(t, err)

	// Verify running
	running, err := env.IsRunning(ctx)
	require.NoError(t, err)
	assert.True(t, running)

	// Send command
	err = env.SendCommand("echo 'manta-test-output'")
	assert.NoError(t, err)

	// Read logs
	time.Sleep(500 * time.Millisecond)
	logs, err := env.Readlog(10)
	assert.NoError(t, err)
	assert.NotEmpty(t, logs)

	// Uptime
	uptime, err := env.Uptime(ctx)
	assert.NoError(t, err)
	assert.True(t, uptime >= 0)

	// Signal container
	err = env.SignalContainer(ctx, "SIGUSR1")
	assert.NoError(t, err)

	// Stop container
	err = env.Stop(ctx)
	assert.NoError(t, err)

	// Destroy
	err = env.Destroy()
	require.NoError(t, err)
}

func TestDockerStopConfigurationsAndTerminate(t *testing.T) {
	dataDir := t.TempDir()
	cfg := &config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	cfg.Docker.Network.Mode = "bridge"
	cfg.Docker.TmpfsSize = 100
	cfg.System.User.Uid = 0
	cfg.System.User.Gid = 0
	config.Set(cfg)

	envCfg := environment.NewConfiguration(environment.Settings{
		Mounts: []environment.Mount{
			{Target: "/home/container", Source: dataDir, ReadOnly: false},
		},
	}, []string{})

	t.Run("stop with command", func(t *testing.T) {
		meta := &Metadata{
			Image: "ghcr.io/pterodactyl/installers:alpine",
			Stop:  remote.ProcessStopConfiguration{Type: remote.ProcessStopCommand, Value: "exit 0"},
		}
		env, err := New("test-stop-cmd", meta, envCfg)
		require.NoError(t, err)
		env.SetLogCallback(func([]byte) {})
		defer func() { _ = env.Destroy() }()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		require.NoError(t, env.Start(ctx))
		require.NoError(t, env.Stop(ctx))
	})

	t.Run("stop with various signals", func(t *testing.T) {
		for _, sig := range []string{"SIGABRT", "SIGINT", "C", "SIGKILL", "UNKNOWN"} {
			meta := &Metadata{
				Image: "ghcr.io/pterodactyl/installers:alpine",
				Stop:  remote.ProcessStopConfiguration{Type: remote.ProcessStopSignal, Value: sig},
			}
			env, err := New("test-stop-sig-"+sig, meta, envCfg)
			require.NoError(t, err)
			env.SetLogCallback(func([]byte) {})
			defer func() { _ = env.Destroy() }()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			require.NoError(t, env.Start(ctx))
			require.NoError(t, env.Stop(ctx))
		}
	})

	t.Run("stop with native docker stop", func(t *testing.T) {
		meta := &Metadata{
			Image: "ghcr.io/pterodactyl/installers:alpine",
			Stop:  remote.ProcessStopConfiguration{Type: ""},
		}
		env, err := New("test-stop-native-nonexistent", meta, envCfg)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Stop on non-existent container exercises the fallback path and not-found handling
		err = env.Stop(ctx)
		assert.NoError(t, err)
		assert.Equal(t, environment.ProcessOfflineState, env.State())
	})

	t.Run("terminate", func(t *testing.T) {
		meta := &Metadata{
			Image: "ghcr.io/pterodactyl/installers:alpine",
			Stop:  remote.ProcessStopConfiguration{Type: "stop"},
		}
		env, err := New("test-terminate", meta, envCfg)
		require.NoError(t, err)
		env.SetLogCallback(func([]byte) {})
		defer func() { _ = env.Destroy() }()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		require.NoError(t, env.Start(ctx))
		require.NoError(t, env.Terminate(ctx, "SIGKILL"))
	})

	t.Run("wait for stop", func(t *testing.T) {
		meta := &Metadata{
			Image: "ghcr.io/pterodactyl/installers:alpine",
			Stop:  remote.ProcessStopConfiguration{Type: remote.ProcessStopSignal, Value: "SIGKILL"},
		}
		env, err := New("test-wait-for-stop", meta, envCfg)
		require.NoError(t, err)
		env.SetLogCallback(func([]byte) {})
		defer func() { _ = env.Destroy() }()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		require.NoError(t, env.Start(ctx))
		require.NoError(t, env.WaitForStop(ctx, 3*time.Second, true))
	})
}

func TestDockerHelpersAndBurst(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "tok"})

	// 1. noopWriter
	nw := noopWriter{}
	n, err := nw.Write([]byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, 4, n)

	// 2. parseErrorFromResponse
	res200 := &http.Response{StatusCode: http.StatusOK}
	assert.NoError(t, parseErrorFromResponse(res200, nil))

	res404 := &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
	err = parseErrorFromResponse(res404, []byte(`{"message":"no such container"}`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no such container")

	res500 := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
	}
	err = parseErrorFromResponse(res500, []byte("server error"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "server error")

	// 3. cgroup burst helpers
	entry := log.WithField("test", "burst")
	writeCpuBurst(entry, -1, 100)
	writeCpuBurst(entry, os.Getpid(), 100)
	logBurstFailure(entry, 100)
	logBurstFailure(entry, 0)

	SetCpuBurst(context.Background(), nil, "nonexistent-container", 0)

	cfg := config.Get()
	cfg.Docker.CpuBurst.Enabled = false
	config.Set(cfg)
	SetCpuBurst(context.Background(), nil, "nonexistent-container", 1000)

	cfg.Docker.CpuBurst.Enabled = true
	cfg.Docker.CpuBurst.Percent = 50
	config.Set(cfg)
	if dcli, err := environment.Docker(); err == nil {
		SetCpuBurst(context.Background(), dcli, "nonexistent-container", 1000)
	}
}

func TestDockerPowerAndImageHelpers(t *testing.T) {
	config.Set(&config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	})
	cfg := environment.NewConfiguration(environment.Settings{}, []string{})
	meta := &Metadata{Image: "alpine:latest"}
	env, err := New("test-server-power-helpers", meta, cfg)
	require.NoError(t, err)

	dcli, err := environment.Docker()
	require.NoError(t, err)
	env.client = dcli

	// 1. ensureImageExists with local image prefix "~"
	assert.NoError(t, env.ensureImageExists("~local-image:latest"))

	// 2. SignalContainer on nonexistent container returns nil
	assert.NoError(t, env.SignalContainer(context.Background(), "SIGTERM"))

	// 3. Terminate on nonexistent container returns nil
	assert.NoError(t, env.Terminate(context.Background(), "SIGKILL"))
	assert.Equal(t, environment.ProcessOfflineState, env.State())

	// 4. WaitForStop with canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = env.WaitForStop(ctx, 10*time.Millisecond, true)

	// 5. Exists, IsRunning, ExitState, ContainerInspect on nonexistent container
	exists, err := env.Exists()
	assert.NoError(t, err)
	assert.False(t, exists)

	running, err := env.IsRunning(context.Background())
	assert.Error(t, err)
	assert.False(t, running)

	exitCode, oom, err := env.ExitState()
	assert.NoError(t, err)
	assert.Equal(t, uint32(1), exitCode)
	assert.False(t, oom)

	_, err = env.ContainerInspect(context.Background())
	assert.Error(t, err)

	// InSituUpdate on nonexistent container returns nil
	assert.NoError(t, env.InSituUpdate())

	// 6. parseErrorFromResponse cases
	res200 := &http.Response{StatusCode: 200}
	assert.NoError(t, parseErrorFromResponse(res200, []byte("ok")))

	res404 := &http.Response{
		StatusCode: 404,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
	assert.Error(t, parseErrorFromResponse(res404, []byte(`{"message":"not found"}`)))

	res500 := &http.Response{
		StatusCode: 500,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
	}
	assert.Error(t, parseErrorFromResponse(res500, []byte("internal server error")))

	resBadJson := &http.Response{
		StatusCode: 500,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
	assert.Error(t, parseErrorFromResponse(resBadJson, []byte("{broken-json")))
}

func TestDockerStartFailure(t *testing.T) {
	config.Set(&config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	})
	cfg := environment.NewConfiguration(environment.Settings{}, []string{})
	meta := &Metadata{Image: "ghcr.io/invalid/nonexistent-image:invalid"}
	env, err := New("test-server-start-fail", meta, cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = env.Start(ctx)
	assert.Error(t, err)
	assert.Equal(t, environment.ProcessOfflineState, env.State())
}







