package docker

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/environment"
)

// Preserve the former full-SDK calculation as an independent regression oracle.
func sdkResourceStats(frame []byte, uptime int64) (environment.Stats, error) {
	var v container.StatsResponse
	if err := json.Unmarshal(frame, &v); err != nil {
		return environment.Stats{}, err
	}
	memory := v.MemoryStats.Usage
	if cache, present := v.MemoryStats.Stats["total_inactive_file"]; present && cache < memory {
		memory -= cache
	} else if cache := v.MemoryStats.Stats["inactive_file"]; cache < memory {
		memory -= cache
	}
	if !v.PreRead.IsZero() {
		uptime += v.Read.Sub(v.PreRead).Milliseconds()
	}
	out := environment.Stats{Uptime: uptime, Memory: memory, MemoryLimit: v.MemoryStats.Limit,
		CpuAbsolute: calculateDockerAbsoluteCpu(v.PreCPUStats, v.CPUStats)}
	for _, network := range v.Networks {
		out.Network.RxBytes += network.RxBytes
		out.Network.TxBytes += network.TxBytes
	}
	return out, nil
}

func TestResourceStatsMatchesSDKCounters(t *testing.T) {
	fixture, err := os.ReadFile("testdata/stats-v2.json")
	require.NoError(t, err)
	cases := map[string]string{
		"captured v2":          string(fixture),
		"v1 cache":             `{"memory_stats":{"usage":1000,"limit":2000,"stats":{"total_inactive_file":200,"inactive_file":400}}}`,
		"zero v1 cache":        `{"memory_stats":{"usage":1000,"stats":{"total_inactive_file":0,"inactive_file":400}}}`,
		"null v1 cache":        `{"memory_stats":{"usage":1000,"stats":{"total_inactive_file":null,"inactive_file":400}}}`,
		"cache above usage":    `{"memory_stats":{"usage":1000,"stats":{"total_inactive_file":2000,"inactive_file":400}}}`,
		"v2 cache above usage": `{"memory_stats":{"usage":1000,"stats":{"inactive_file":2000}}}`,
		"missing counters":     `{}`,
		"multiple networks":    `{"networks":{"eth0":{"rx_bytes":9007199254740993,"tx_bytes":5},"eth1":{"rx_bytes":7,"tx_bytes":8}}}`,
		"CPU fallback":         `{"cpu_stats":{"cpu_usage":{"total_usage":200,"percpu_usage":[1,2,3,4]},"system_cpu_usage":2000},"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000}}`,
		"CPU online cores":     `{"cpu_stats":{"cpu_usage":{"total_usage":200},"system_cpu_usage":2000,"online_cpus":4},"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000}}`,
		"CPU counter reset":    `{"cpu_stats":{"cpu_usage":{"total_usage":50},"system_cpu_usage":2000,"online_cpus":4},"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000}}`,
		"read interval":        `{"read":"2026-10-15T00:00:02Z","preread":"2026-10-15T00:00:00Z"}`,
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			expected, err := sdkResourceStats([]byte(frame), 1234)
			require.NoError(t, err)
			actual, err := decodeResourceStats(json.NewDecoder(strings.NewReader(frame)), 1234)
			require.NoError(t, err)
			require.Equal(t, expected, actual)
		})
	}
}

func TestResourceStatsStreamDoesNotRetainPreviousCounters(t *testing.T) {
	first := `{"memory_stats":{"usage":1000,"stats":{"total_inactive_file":200}},"networks":{"eth0":{"rx_bytes":20}}}`
	second := `{"memory_stats":{"usage":500,"stats":{"inactive_file":100}}}`
	dec := json.NewDecoder(strings.NewReader(first + "\n" + second))
	for _, frame := range []string{first, second} {
		expected, err := sdkResourceStats([]byte(frame), 100)
		require.NoError(t, err)
		actual, err := decodeResourceStats(dec, 100)
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	}
	_, err := decodeResourceStats(dec, 100)
	require.ErrorIs(t, err, io.EOF)
}

func TestResourceStatsRejectsInvalidUsedCounters(t *testing.T) {
	for _, frame := range []string{
		`{"memory_stats":{"usage":-1}}`, `{"memory_stats":{"stats":{"total_inactive_file":"bad"}}}`,
		`{"networks":{"eth0":{"rx_bytes":18446744073709551616}}}`, `{"read":"invalid"}`, `{"cpu_stats":`,
	} {
		_, err := decodeResourceStats(json.NewDecoder(bytes.NewBufferString(frame)), 100)
		require.Error(t, err)
	}
}
