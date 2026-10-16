package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/server/filesystem"
	"github.com/pterodactyl/wings/system"
)

func TestResourceEventCountersAndObserverDelivery(t *testing.T) {
	for _, observed := range []bool{false, true} {
		name := "unobserved"
		if observed {
			name = "observed"
		}
		t.Run(name, func(t *testing.T) {
			s := newResourceBenchmarkServer(t)
			c := make(chan []byte, 1)
			if observed {
				s.Events().On(c)
			}
			want := environment.Stats{Memory: 9007199254740993, MemoryLimit: ^uint64(0), CpuAbsolute: 12.763,
				Network: environment.NetworkStats{RxBytes: ^uint64(0), TxBytes: 9007199254740995}, Uptime: 7365124}
			data, err := json.Marshal(events.Event{Topic: environment.ResourceEvent, Data: want})
			require.NoError(t, err)
			s.handleEnvironmentEvent(data, newDiskLimiter(s))
			require.Equal(t, want, s.Proc().Stats)
			if !observed {
				return
			}
			var got struct {
				Topic string
				Data  struct {
					environment.Stats
					State string `json:"state"`
					Disk  int64  `json:"disk_bytes"`
				}
			}
			select {
			case b := <-c:
				require.NoError(t, events.DecodeTo(b, &got))
			default:
				t.Fatal("subscribed observer missed stats")
			}
			require.Equal(t, StatsEvent, got.Topic)
			require.Equal(t, want, got.Data.Stats)
			require.Equal(t, environment.ProcessRunningState, got.Data.State)
		})
	}
}

func TestResourceEventFreshFieldsAndFieldOrder(t *testing.T) {
	for _, tc := range []struct {
		data   string
		memory uint64
	}{
		{`{"Data":{"memory_bytes":7},"Topic":"resources"}`, 7},
		{`{"Topic":"resources","Data":{}}`, 0},
		{`{"Topic":"resources","Data":null}`, 0},
		{`{"Topic":"resources"}`, 0},
	} {
		s := newResourceBenchmarkServer(t)
		s.resources.UpdateStats(environment.Stats{Memory: 99, MemoryLimit: 101, Network: environment.NetworkStats{RxBytes: 88}})
		s.handleEnvironmentEvent([]byte(tc.data), newDiskLimiter(s))
		want := environment.Stats{Memory: tc.memory}
		require.Equal(t, want, s.Proc().Stats)
	}
}

func TestMalformedResourceEventsDoNotReplaceStats(t *testing.T) {
	s := newResourceBenchmarkServer(t)
	want := environment.Stats{Memory: 99}
	s.resources.UpdateStats(want)
	for _, data := range []string{`{`, `{"Topic":7}`, `{"Topic":"resources","Data":{"memory_bytes":"invalid"}}`, `{"Topic":"resources","Data":{"network":{"tx_bytes":-1}}}`, `{"Topic":"resources","Data":[]} {}`} {
		s.handleEnvironmentEvent([]byte(data), newDiskLimiter(s))
		require.Equal(t, want, s.Proc().Stats)
	}
}

type diskLimitedEnvironment struct {
	*stateEnvironment
	stops int
}

func (e *diskLimitedEnvironment) WaitForStop(context.Context, time.Duration, bool) error {
	e.stops++
	return nil
}

func TestResourceDiskLimitAndStartupResetWithoutObservers(t *testing.T) {
	for _, stateData := range []string{`"starting"`, `"\u0073tarting"`} {
		s := newResourceBenchmarkServer(t)
		cfg := config.Get()
		cfg.System.DiskCheckInterval = 150
		config.Set(cfg)
		var err error
		s.fs, err = filesystem.New(t.TempDir(), 1, nil)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(s.fs.Path(), "full"), []byte("over the limit"), 0o600))
		_, err = s.fs.DiskUsage(false)
		require.NoError(t, err)
		env := &diskLimitedEnvironment{stateEnvironment: &stateEnvironment{state: environment.ProcessRunningState}}
		s.Environment = env
		limit := newDiskLimiter(s)
		s.handleEnvironmentEvent(resourceEventFixture, limit)
		s.handleEnvironmentEvent(resourceEventFixture, limit)
		require.Equal(t, 1, env.stops, "disk stop must run once per start")
		throttle := s.Throttler()
		throttle.limit = system.NewRate(1, time.Hour)
		throttle.strike = nil
		require.True(t, throttle.Allow())
		require.False(t, throttle.Allow())
		env.SetState(environment.ProcessStartingState)
		s.handleEnvironmentEvent([]byte(`{"Topic":"state change","Data":`+stateData+`}`), limit)
		require.True(t, throttle.Allow(), "startup should reset console throttling")
		s.handleEnvironmentEvent(resourceEventFixture, limit)
		require.Equal(t, 2, env.stops, "startup should reset the disk stop limiter")
	}
}

func TestEnvironmentPullStatusPreservesWireValues(t *testing.T) {
	for _, payload := range []json.RawMessage{json.RawMessage(`"Downloading layer \u96ea"`), json.RawMessage(`{"progress":20}`), json.RawMessage(`null`)} {
		s := newResourceBenchmarkServer(t)
		c := make(chan []byte, 1)
		s.Events().On(c)
		data, err := json.Marshal(events.Event{Topic: environment.DockerImagePullStatus, Data: payload})
		require.NoError(t, err)
		s.handleEnvironmentEvent(data, newDiskLimiter(s))
		var got events.Event
		select {
		case b := <-c:
			require.NoError(t, events.DecodeTo(b, &got))
		default:
			t.Fatal("pull progress was not forwarded")
		}
		var want any
		require.NoError(t, json.Unmarshal(payload, &want))
		require.Equal(t, InstallOutputEvent, got.Topic)
		require.Equal(t, want, got.Data)
	}
}
