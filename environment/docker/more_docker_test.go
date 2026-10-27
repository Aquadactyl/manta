package docker

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/apex/log"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
)

func TestDockerHttpStatusErrorAndApi(t *testing.T) {
	config.Set(&config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	})
	// 1. statusError helper
	assert.Nil(t, statusError(nil, 200))
	se404 := statusError(errors.New("not found"), http.StatusNotFound)
	assert.Error(t, se404)
	assert.True(t, cerrdefs.IsNotFound(se404))
	assert.Equal(t, "not found", se404.Error())
	assert.Equal(t, "not found", errors.Unwrap(se404).Error())

	se500 := statusError(errors.New("server error"), http.StatusInternalServerError)
	assert.False(t, cerrdefs.IsNotFound(se500))

	// 2. parseErrorFromResponse with 200-399
	assert.NoError(t, parseErrorFromResponse(&http.Response{StatusCode: 302}, nil))
	assert.Error(t, parseErrorFromResponse(&http.Response{
		StatusCode: 500,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, []byte("invalid-json")))
	assert.Error(t, parseErrorFromResponse(&http.Response{
		StatusCode: 500,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
	}, []byte("plain text error")))

	// 3. Fast enabled vs disabled ContainerInspect
	cfg := environment.NewConfiguration(environment.Settings{}, []string{})
	meta := &Metadata{Image: "alpine:latest"}
	env, err := New("test-inspect-modes", meta, cfg)
	require.NoError(t, err)

	fastEnabled = false
	_, err = env.ContainerInspect(context.Background())
	assert.Error(t, err)
	assert.True(t, cerrdefs.IsNotFound(err))

	fastEnabled = true
	_, err = env.ContainerInspect(context.Background())
	assert.Error(t, err)
	assert.True(t, cerrdefs.IsNotFound(err))

	cancCtx, cancelFn := context.WithCancel(context.Background())
	cancelFn()
	_, err = env.ContainerInspect(cancCtx)
	assert.Error(t, err)
	assert.False(t, cerrdefs.IsNotFound(err))
}

func TestDockerStatsUptimeAndPollResources(t *testing.T) {
	cfg := environment.NewConfiguration(environment.Settings{}, []string{})
	meta := &Metadata{Image: "alpine:latest"}
	env, err := New("test-stats-polling", meta, cfg)
	require.NoError(t, err)

	// pollResources on offline server
	env.SetState(environment.ProcessOfflineState)
	assert.EqualError(t, env.pollResources(context.Background()), "cannot enable resource polling on a stopped server")

	// Uptime on stopped server
	ctx := context.Background()
	uptime, err := env.Uptime(ctx)
	assert.Error(t, err)
	assert.Equal(t, int64(0), uptime)
}

func TestDockerSendCommandAndReattach(t *testing.T) {
	cfg := environment.NewConfiguration(environment.Settings{}, []string{})
	meta := &Metadata{
		Image: "alpine:latest",
		Stop:  remote.ProcessStopConfiguration{Type: "command", Value: "stop"},
	}
	env, err := New("test-send-command", meta, cfg)
	require.NoError(t, err)

	// Control characters in SendCommand
	assert.Error(t, env.SendCommand("test\x00command"))
	assert.Error(t, env.SendCommand("test\x1fcommand"))
	assert.Error(t, env.SendCommand("test\x7fcommand"))

	// SendCommand when not attached
	assert.ErrorIs(t, env.SendCommand("valid command"), ErrNotAttached)

	// reattach when not running returns false
	assert.False(t, env.reattach())
}

func TestDockerPowerStatesAndOnBeforeStart(t *testing.T) {
	dataDir := t.TempDir()
	c := &config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	c.Docker.Network.Mode = "bridge"
	c.Docker.TmpfsSize = 100
	c.System.User.Uid = 0
	c.System.User.Gid = 0
	config.Set(c)

	envCfg := environment.NewConfiguration(environment.Settings{
		Mounts: []environment.Mount{
			{Target: "/home/container", Source: dataDir, ReadOnly: false},
		},
	}, []string{})

	meta := &Metadata{
		Image: "ghcr.io/pterodactyl/installers:alpine",
		Stop:  remote.ProcessStopConfiguration{Type: "stop"},
	}

	env, err := New("test-power-before-start", meta, envCfg)
	require.NoError(t, err)
	defer func() { _ = env.Destroy() }()

	// Stop when already offline returns nil
	env.SetState(environment.ProcessOfflineState)
	assert.NoError(t, env.Stop(context.Background()))

	// WaitForStop when already offline returns nil
	assert.NoError(t, env.WaitForStop(context.Background(), time.Second, false))

	// OnBeforeStart
	assert.NoError(t, env.OnBeforeStart(context.Background()))

	// Start container with fresh context
	startCtx, startCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer startCancel()
	require.NoError(t, env.Start(startCtx))

	// Start when already starting or running returns nil
	assert.NoError(t, env.Start(context.Background()))

	// Clean up with Terminate
	assert.NoError(t, env.Terminate(context.Background(), "SIGKILL"))
	assert.NoError(t, env.Destroy())
}

func TestDockerCgroupBurstDetails(t *testing.T) {
	// 1. cpuBurstMicroseconds
	assert.Equal(t, int64(0), cpuBurstMicroseconds(0, 50))
	assert.Equal(t, int64(0), cpuBurstMicroseconds(100, 0))
	assert.Equal(t, int64(0), cpuBurstMicroseconds(-10, 50))
	assert.Equal(t, int64(50), cpuBurstMicroseconds(100, 50))
	assert.Equal(t, int64(100), cpuBurstMicroseconds(100, 150)) // clamped to 100%

	// 2. resolveCgroupCpuFile
	v2Input := "0::/docker/abc123\n"
	f, err := resolveCgroupCpuFile(v2Input, true)
	require.NoError(t, err)
	assert.Equal(t, "/sys/fs/cgroup/docker/abc123/cpu.max.burst", f)

	v1Input := "1:cpu,cpuacct:/docker/abc123\n"
	f, err = resolveCgroupCpuFile(v1Input, false)
	require.NoError(t, err)
	assert.Equal(t, "/sys/fs/cgroup/cpu/docker/abc123/cpu.cfs_burst_us", f)

	// Invalid lines
	_, err = resolveCgroupCpuFile("invalid-line\n", true)
	assert.Error(t, err)
	_, err = resolveCgroupCpuFile("0::not-absolute\n", true)
	assert.Error(t, err)
	_, err = resolveCgroupCpuFile("0::/path/with/../traversal\n", true)
	assert.Error(t, err)
	_, err = resolveCgroupCpuFile("1:memory:/docker/abc123\n", false)
	assert.Error(t, err)

	// writeBurstFile error on bad pid
	assert.Error(t, writeBurstFile(-999999, 1000))
}

func TestDockerCreateVariations(t *testing.T) {
	dataDir := t.TempDir()
	c := &config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	c.Docker.Network.Mode = "bridge"
	c.Docker.TmpfsSize = 100
	c.System.User.Uid = 0
	c.System.User.Gid = 0
	c.System.User.Rootless.Enabled = true
	c.System.User.Rootless.ContainerUID = 1000
	c.System.User.Rootless.ContainerGID = 1000
	config.Set(c)

	alloc := environment.Allocations{
		ForceOutgoingIP: false,
	}
	alloc.DefaultMapping.Ip = "127.0.0.1"
	alloc.DefaultMapping.Port = 25565

	envCfg := environment.NewConfiguration(environment.Settings{
		Mounts: []environment.Mount{
			{Target: "/home/container", Source: dataDir, ReadOnly: false},
		},
		Allocations: alloc,
	}, []string{"SERVER_IP=127.0.0.1"})

	meta := &Metadata{
		Image: "~ghcr.io/pterodactyl/installers:alpine",
		Stop:  remote.ProcessStopConfiguration{Type: "stop"},
	}

	env, err := New("test-create-variations", meta, envCfg)
	require.NoError(t, err)
	defer func() { _ = env.Destroy() }()

	require.NoError(t, env.Create())
	assert.NoError(t, env.Destroy())

	// ForceOutgoingIP create attempt
	allocForce := environment.Allocations{ForceOutgoingIP: true}
	allocForce.DefaultMapping.Ip = "127.0.0.1"
	allocForce.DefaultMapping.Port = 25566
	envCfgForce := environment.NewConfiguration(environment.Settings{
		Mounts:      []environment.Mount{{Target: "/home/container", Source: dataDir}},
		Allocations: allocForce,
	}, []string{})
	envForce, err := New("test-create-force-ip", meta, envCfgForce)
	require.NoError(t, err)
	defer func() { _ = envForce.Destroy() }()
	_ = envForce.Create()
	_ = envForce.Destroy()
}

func TestDockerReattachOffline(t *testing.T) {
	cfg := environment.NewConfiguration(environment.Settings{}, []string{})
	meta := &Metadata{Image: "alpine:latest"}
	env, err := New("test-reattach-offline", meta, cfg)
	require.NoError(t, err)

	// When container is not running, reattach returns false
	assert.False(t, env.reattach())

	// Attach on nonexistent container returns error
	assert.Error(t, env.Attach(context.Background()))

	// Readlog on nonexistent container returns error
	_, err = env.Readlog(10)
	assert.Error(t, err)
}

func TestDockerEnsureImageExistsLocal(t *testing.T) {
	c := config.Get()
	if c.Docker.Registries == nil {
		c.Docker.Registries = make(map[string]config.RegistryConfiguration)
	}
	c.Docker.Registries["registry.example.com"] = config.RegistryConfiguration{
		Username: "testuser",
		Password: "testpassword",
	}
	config.Set(c)

	cfg := environment.NewConfiguration(environment.Settings{}, []string{})
	meta := &Metadata{Image: "ghcr.io/pterodactyl/installers:alpine"}
	env, err := New("test-image-exists", meta, cfg)
	require.NoError(t, err)

	// Local tag matching existing image
	assert.NoError(t, env.ensureImageExists("ghcr.io/pterodactyl/installers:alpine"))

	// Non-existent image returns error after checking local images
	assert.Error(t, env.ensureImageExists("registry.example.com/nonexistent/image:1.0"))
}

func TestDockerExistingContainerOperations(t *testing.T) {
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
		Limits: environment.Limits{
			CpuLimit:    100,
			MemoryLimit: 512,
		},
	}, []string{})
	meta := &Metadata{
		Image: "ghcr.io/pterodactyl/installers:alpine",
		Stop:  remote.ProcessStopConfiguration{Type: remote.ProcessStopSignal, Value: "SIGKILL"},
	}

	containerID := "test-existing-" + uuid.New().String()
	env, err := New(containerID, meta, envCfg)
	require.NoError(t, err)
	defer func() { _ = env.Destroy() }()

	require.NoError(t, env.Create())

	// Exists
	exists, err := env.Exists()
	assert.NoError(t, err)
	assert.True(t, exists)

	// ExitState
	exitCode, oom, err := env.ExitState()
	assert.NoError(t, err)
	assert.Equal(t, uint32(0), exitCode)
	assert.False(t, oom)

	// IsRunning
	running, err := env.IsRunning(context.Background())
	assert.NoError(t, err)
	assert.False(t, running)

	// Readlog
	logs, err := env.Readlog(5)
	assert.NoError(t, err)
	assert.Empty(t, logs)

	// ContainerInspect fastEnabled true and false
	fastEnabled = true
	insp1, err := env.ContainerInspect(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, insp1)

	fastEnabled = false
	insp2, err := env.ContainerInspect(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, insp2)

	// Create returns nil when container already exists
	assert.NoError(t, env.Create())

	// InSituUpdate updates resource limits
	assert.NoError(t, env.InSituUpdate())

	// SignalContainer when stopped sets stopping and offline
	env.SetState(environment.ProcessStartingState)
	assert.NoError(t, env.SignalContainer(context.Background(), "SIGTERM"))
	assert.Equal(t, environment.ProcessOfflineState, env.State())

	// WaitForStop when already stopped returns nil
	assert.NoError(t, env.WaitForStop(context.Background(), 200*time.Millisecond, true))
}

func TestDockerCgroupBurstMore(t *testing.T) {
	// 1. writeCpuBurst edge cases
	l := log.WithField("test", true)
	writeCpuBurst(l, -1, 100)
	writeCpuBurst(l, 0, 100)
	writeCpuBurst(l, 999999999, 100)
	writeCpuBurst(l, 999999999, 0)

	// 2. SetCpuBurst with enabled burst
	c := &config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	c.Docker.CpuBurst.Enabled = true
	c.Docker.CpuBurst.Percent = 50
	config.Set(c)
	defer func() {
		c.Docker.CpuBurst.Enabled = false
		config.Set(c)
	}()

	burstCfg := environment.NewConfiguration(environment.Settings{}, []string{})
	burstMeta := &Metadata{Image: "ghcr.io/pterodactyl/installers:alpine"}
	burstContainerID := "test-burst-" + uuid.New().String()
	env, err := New(burstContainerID, burstMeta, burstCfg)
	require.NoError(t, err)

	SetCpuBurst(context.Background(), env.client, burstContainerID, 1000)
	SetCpuBurst(context.Background(), env.client, "nonexistent-container-uuid", 1000)
	SetCpuBurst(context.Background(), env.client, burstContainerID, 0)

	// 3. resolveCgroupCpuFile parsing variations
	_, err = resolveCgroupCpuFile("1:memory:/docker/123", false)
	assert.Error(t, err)
	_, err = resolveCgroupCpuFile("1:cpu:relative/path", false)
	assert.Error(t, err)
	_, err = resolveCgroupCpuFile("1:cpu:/../escape", false)
	assert.Error(t, err)
	_, err = resolveCgroupCpuFile("1:cpu:/docker/123", true)
	assert.Error(t, err)

	v2Path, err := resolveCgroupCpuFile("0::/docker.slice/docker-123.scope", true)
	assert.NoError(t, err)
	assert.Equal(t, "/sys/fs/cgroup/docker.slice/docker-123.scope/cpu.max.burst", v2Path)
}

func TestDockerRunningContainerOperations(t *testing.T) {
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
		Stop:  remote.ProcessStopConfiguration{Type: remote.ProcessStopSignal, Value: "SIGKILL"},
	}
	env, err := New("test-docker-running-ops", meta, envCfg)
	require.NoError(t, err)
	env.SetLogCallback(func([]byte) {})
	defer func() { _ = env.Destroy() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, env.Start(ctx))

	// 1. Uptime on running container
	uptime, err := env.Uptime(ctx)
	assert.NoError(t, err)
	assert.True(t, uptime >= 0)

	// Exists and IsRunning and InSituUpdate on running container
	exists, err := env.Exists()
	assert.NoError(t, err)
	assert.True(t, exists)

	isRunning, err := env.IsRunning(ctx)
	assert.NoError(t, err)
	assert.True(t, isRunning)

	assert.NoError(t, env.InSituUpdate())

	// Attach when already attached returns nil immediately
	assert.NoError(t, env.Attach(ctx))

	// Listen for resource event
	ch := make(chan []byte, 10)
	env.Events().On(ch)
	defer env.Events().Off(ch)
	select {
	case <-ch:
	case <-time.After(500 * time.Millisecond):
	}
	// Test offline check in pollResources
	env.SetState(environment.ProcessOfflineState)
	time.Sleep(50 * time.Millisecond)
	env.SetState(environment.ProcessRunningState)

	// 2. Start when container is already running (exercises Attach and applyCpuBurst)
	assert.NoError(t, env.Start(ctx))

	// 3. Reattach when container is running
	assert.True(t, env.reattach())

	// 4. Reattach exceeding threshold (terminates container)
	env.reattachedCount = 4
	assert.True(t, env.reattach())

	// 5. WaitForStop with canceled context
	cancCtx, cancelFn := context.WithCancel(context.Background())
	cancelFn()
	assert.Error(t, env.WaitForStop(cancCtx, 50*time.Millisecond, false))
	assert.Error(t, env.WaitForStop(cancCtx, 50*time.Millisecond, true))

	// Non-existent environment Exists, ExitState, InSituUpdate, OnBeforeStart
	nonEnv, err := New("test-nonexistent-uuid-9999", meta, envCfg)
	require.NoError(t, err)
	nonExists, err := nonEnv.Exists()
	assert.NoError(t, err)
	assert.False(t, nonExists)

	exitCode, oom, err := nonEnv.ExitState()
	assert.NoError(t, err)
	assert.Equal(t, uint32(1), exitCode)
	assert.False(t, oom)

	assert.NoError(t, nonEnv.InSituUpdate())

	_ = env.Destroy()
}
