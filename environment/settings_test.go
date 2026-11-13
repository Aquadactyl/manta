package environment

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

func TestLimitsAndSettings(t *testing.T) {
	config.Set(&config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	})
	config.Update(func(c *config.Configuration) {
		c.Docker.Overhead.Override = false
		c.Docker.ContainerPidLimit = 512
		c.Docker.CpuShares = 1024
		c.Docker.CpuPeriod = 100000
	})

	// 1. ConvertedCpuLimit
	lUnlimited := Limits{CpuLimit: 0}
	assert.Equal(t, int64(-1), lUnlimited.ConvertedCpuLimit())

	lLimited := Limits{CpuLimit: 200}
	assert.Equal(t, int64(200000), lLimited.ConvertedCpuLimit())

	// 2. MemoryOverheadMultiplier and BoundedMemoryLimit
	lMem := Limits{MemoryLimit: 1024} // < 2G -> 1.15
	assert.Equal(t, 1.15, lMem.MemoryOverheadMultiplier())
	assert.Equal(t, int64(math.Round(1.15*1024*1024*1024)), lMem.BoundedMemoryLimit())

	// 3. ConvertedSwap
	lSwapNeg := Limits{MemoryLimit: 1024, Swap: -1}
	assert.Equal(t, int64(-1), lSwapNeg.ConvertedSwap())

	lSwapPos := Limits{MemoryLimit: 1024, Swap: 512}
	expectedSwap := (512 * 1024 * 1024) + lMem.BoundedMemoryLimit()
	assert.Equal(t, expectedSwap, lSwapPos.ConvertedSwap())

	// 4. ProcessLimit
	assert.Equal(t, int64(512), lMem.ProcessLimit())

	// 5. AsContainerResources
	lFull := Limits{
		CpuLimit:    150,
		MemoryLimit: 2048,
		Swap:        256,
		IoWeight:    500,
		Threads:     "0,1",
		OOMDisabled: true,
	}
	resources := lFull.AsContainerResources()
	assert.Equal(t, lFull.BoundedMemoryLimit(), resources.Memory)
	assert.Equal(t, int64(2048*1024*1024), resources.MemoryReservation)
	assert.True(t, *resources.OomKillDisable)
	assert.Equal(t, "0,1", resources.CpusetCpus)
	assert.Equal(t, int64(150000), resources.CPUQuota)

	// 6. blkioWeightSupported
	_ = blkioWeightSupported()
}

func TestBlkioWeightSupportedAcrossPlatformsAndCgroupLayouts(t *testing.T) {
	const controllers = "/sys/fs/cgroup/cgroup.controllers"
	const systemSliceWeight = "/sys/fs/cgroup/system.slice/io.weight"
	const rootWeight = "/sys/fs/cgroup/io.weight"

	tests := []struct {
		name  string
		goos  string
		files map[string]error
		want  bool
	}{
		{name: "darwin does not probe the host", goos: "darwin", want: false},
		{name: "cgroup v1", goos: "linux", files: map[string]error{controllers: os.ErrNotExist}, want: true},
		{name: "delegated cgroup v2 weight", goos: "linux", files: map[string]error{controllers: nil, systemSliceWeight: nil}, want: true},
		{name: "root cgroup v2 weight", goos: "linux", files: map[string]error{controllers: nil, systemSliceWeight: os.ErrNotExist, rootWeight: nil}, want: true},
		{name: "cgroup v2 without weight support", goos: "linux", files: map[string]error{controllers: nil, systemSliceWeight: os.ErrNotExist, rootWeight: os.ErrNotExist}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			stat := func(path string) (os.FileInfo, error) {
				calls++
				if err, ok := tt.files[path]; ok {
					return nil, err
				}
				return nil, os.ErrNotExist
			}
			assert.Equal(t, tt.want, blkioWeightSupportedFor(tt.goos, stat))
			if tt.goos == "darwin" {
				assert.Zero(t, calls)
			}
		})
	}
}

func TestVariablesGet(t *testing.T) {
	v := Variables{
		"int":     int(10),
		"int32":   int32(20),
		"int64":   int64(30),
		"float32": float32(40.5),
		"float64": float64(50.5),
		"bool":    true,
		"str":     "hello",
		"nil":     nil,
		"bad":     make(chan int),
	}

	assert.Equal(t, "10", v.Get("int"))
	assert.Equal(t, "20", v.Get("int32"))
	assert.Equal(t, "30", v.Get("int64"))
	assert.Contains(t, v.Get("float32"), "40.5")
	assert.Contains(t, v.Get("float64"), "50.5")
	assert.Equal(t, "true", v.Get("bool"))
	assert.Equal(t, "hello", v.Get("str"))
	assert.Empty(t, v.Get("nil"))
	assert.Empty(t, v.Get("nonexistent"))
	assert.Empty(t, v.Get("bad"))
}

func TestConfigurationStruct(t *testing.T) {
	cfg := NewConfiguration(
		Settings{
			Allocations: Allocations{
				DefaultMapping: struct {
					Ip   string `json:"ip"`
					Port int    `json:"port"`
				}{
					Ip:   "127.0.0.1",
					Port: 25565,
				},
			},
		},
		[]string{"ENV_A=VAL_A"},
	)

	assert.Equal(t, 25565, cfg.Allocations().DefaultMapping.Port)
	assert.Equal(t, []string{"ENV_A=VAL_A"}, cfg.EnvironmentVariables())
	assert.Empty(t, cfg.Mounts())
	assert.Empty(t, cfg.Labels())

	// SetSettings & SetEnvironmentVariables
	cfg.SetSettings(Settings{
		Limits: Limits{MemoryLimit: 4096},
	})
	assert.Equal(t, int64(4096), cfg.Limits().MemoryLimit)

	cfg.SetEnvironmentVariables([]string{"ENV_B=VAL_B"})
	assert.Equal(t, []string{"ENV_B=VAL_B"}, cfg.EnvironmentVariables())

	// Allocations.Exposed
	alloc := Allocations{
		Mappings: map[string][]int{
			"127.0.0.1": {25565},
		},
	}
	exposed := alloc.Exposed()
	assert.NotEmpty(t, exposed)
}

func TestDockerClientAndConfigure(t *testing.T) {
	cli, err := Docker()
	require.NoError(t, err)
	assert.NotNil(t, cli)

	config.Set(&config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	})
	config.Update(func(c *config.Configuration) {
		c.Docker.Network.Name = "bridge"
	})

	err = ConfigureDocker(context.Background())
	require.NoError(t, err)

	// Configure with host network
	config.Update(func(c *config.Configuration) {
		c.Docker.Network.Name = "host"
	})
	err = ConfigureDocker(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", config.Get().Docker.Network.Interface)

	// Configure with canceled context returns error immediately
	ctxCancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err = ConfigureDocker(ctxCancelled)
	assert.Error(t, err)

	// Allocations with invalid port (< 1 or > 65535)
	allocInvalid := Allocations{
		Mappings: map[string][]int{
			"127.0.0.1": {0, 8080, 70000},
		},
	}
	bindings := allocInvalid.Bindings()
	assert.Len(t, bindings, 2) // only tcp and udp for 8080
}
