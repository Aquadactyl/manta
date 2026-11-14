package main

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/pterodactyl/wings/environment"
	"github.com/stretchr/testify/require"
)

type eventTestCloser struct{ closed bool }

func (c *eventTestCloser) Close() error {
	c.closed = true
	return nil
}

func TestEventStreamValidators(t *testing.T) {
	utilization := map[string]json.RawMessage{
		"state": nil, "memory_bytes": nil, "memory_limit_bytes": nil,
		"uptime": nil, "disk_bytes": nil, "cpu_absolute": nil, "network": nil,
	}
	require.NoError(t, validateUtilizationSchema(utilization))
	require.Error(t, validateUtilizationSchema(map[string]json.RawMessage{"state": nil}))

	closer := &eventTestCloser{}
	require.NoError(t, validateSocketAuth(closer, "auth success"))
	require.False(t, closer.closed)
	require.Error(t, validateSocketAuth(closer, "auth failed"))
	require.True(t, closer.closed)

	valid := `{"state":"running","memory_bytes":100,"memory_limit_bytes":200,"uptime":10,"disk_bytes":50,"cpu_absolute":1.5,"network":{"rx_bytes":20,"tx_bytes":30}}`
	usage, err := parseStreamedStats([]string{valid}, utilization)
	require.NoError(t, err)
	require.Equal(t, int64(10), usage.Uptime)
	require.Equal(t, int64(50), usage.Disk)
	for _, tc := range []struct {
		name        string
		args        []string
		utilization map[string]json.RawMessage
	}{
		{name: "argument missing", utilization: utilization},
		{name: "invalid JSON", args: []string{"{"}, utilization: utilization},
		{name: "wrong number of fields", args: []string{`{"state":"running"}`}, utilization: utilization},
		{name: "missing API field", args: []string{valid}, utilization: map[string]json.RawMessage{
			"state": nil, "memory_bytes": nil, "memory_limit_bytes": nil, "uptime": nil,
			"disk_bytes": nil, "cpu_absolute": nil, "missing": nil,
		}},
		{name: "wrong field type", args: []string{`{"state":"running","memory_bytes":"bad","memory_limit_bytes":200,"uptime":10,"disk_bytes":50,"cpu_absolute":1.5,"network":{"rx_bytes":20,"tx_bytes":30}}`}, utilization: utilization},
		{name: "invalid resource values", args: []string{`{"state":"offline","memory_bytes":0,"memory_limit_bytes":0,"uptime":0,"disk_bytes":0,"cpu_absolute":-1,"network":{"rx_bytes":0,"tx_bytes":0}}`}, utilization: utilization},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseStreamedStats(tc.args, tc.utilization)
			require.Error(t, err)
		})
	}

	var first, last environment.Stats
	initial := environment.Stats{Uptime: 10, Network: environment.NetworkStats{RxBytes: 20, TxBytes: 30}}
	require.NoError(t, updateStatsSample(0, &first, &last, initial))
	require.Equal(t, initial, first)
	next := environment.Stats{Uptime: 11, Network: environment.NetworkStats{RxBytes: 21, TxBytes: 31}}
	require.NoError(t, updateStatsSample(1, &first, &last, next))
	require.NoError(t, validateStatsAdvanced(first, last))
	regressed := environment.Stats{Uptime: 9, Network: environment.NetworkStats{RxBytes: 19, TxBytes: 29}}
	require.Error(t, updateStatsSample(2, &first, &last, regressed))
	require.Error(t, validateStatsAdvanced(first, environment.Stats{Uptime: first.Uptime}))
}

var _ io.Closer = (*eventTestCloser)(nil)
