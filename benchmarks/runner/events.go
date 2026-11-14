package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"github.com/pterodactyl/wings/environment"
)

type streamedStats struct {
	environment.Stats
	State string `json:"state"`
	Disk  int64  `json:"disk_bytes"`
}

func validateUtilizationSchema(utilization map[string]json.RawMessage) error {
	if len(utilization) != 7 {
		return errors.New("unexpected API resource schema")
	}
	return nil
}

func validateSocketAuth(c io.Closer, event string) error {
	if event != "auth success" {
		_ = c.Close()
		return errors.New("event stream authentication failed")
	}
	return nil
}

func parseStreamedStats(args []string, utilization map[string]json.RawMessage) (streamedStats, error) {
	if len(args) != 1 {
		return streamedStats{}, errors.New("stats event must carry one string argument")
	}
	data := []byte(args[0])
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return streamedStats{}, err
	}
	if len(keys) != len(utilization) {
		return streamedStats{}, errors.New("stats event schema differs from API")
	}
	for key := range utilization {
		if _, ok := keys[key]; !ok {
			return streamedStats{}, fmt.Errorf("stats event is missing resource field %s", key)
		}
	}
	var usage streamedStats
	if err := json.Unmarshal(data, &usage); err != nil {
		return streamedStats{}, err
	}
	if usage.State != "running" || usage.Memory == 0 || usage.MemoryLimit < usage.Memory || usage.Uptime <= 0 || usage.Disk <= 0 || math.IsNaN(usage.CpuAbsolute) || math.IsInf(usage.CpuAbsolute, 0) || usage.CpuAbsolute < 0 {
		return streamedStats{}, fmt.Errorf("invalid streamed resource values: state=%s memory=%d limit=%d uptime=%d disk=%d cpu=%v", usage.State, usage.Memory, usage.MemoryLimit, usage.Uptime, usage.Disk, usage.CpuAbsolute)
	}
	return usage, nil
}

func updateStatsSample(sample int, first, last *environment.Stats, current environment.Stats) error {
	if sample == 0 {
		*first = current
	} else if current.Uptime < last.Uptime || current.Network.RxBytes < last.Network.RxBytes || current.Network.TxBytes < last.Network.TxBytes {
		return errors.New("streamed resource counters regressed")
	}
	*last = current
	return nil
}

func validateStatsAdvanced(first, last environment.Stats) error {
	if last.Uptime <= first.Uptime {
		return errors.New("streamed uptime did not advance")
	}
	return nil
}

// Check unsolicited stats events: "send stats" replies take a different path
// and would not exercise the optimized bus-to-WebSocket conversion.
func (h *harness) eventStreams() {
	h.startAll()
	for _, variant := range []string{"manta", "wings"} {
		n := h.Nodes[variant]
		for _, s := range n.Servers {
			var api struct {
				Utilization map[string]json.RawMessage `json:"utilization"`
			}
			must(json.Unmarshal(h.api(n, "GET", "/api/servers/"+s.UUID, nil), &api))
			must(validateUtilizationSchema(api.Utilization))
			var wg sync.WaitGroup
			for viewer := range 16 {
				c, event := h.socket(n, s, h.token(n, s, "websocket", nil))
				must(validateSocketAuth(c, event))
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer c.Close()
					must(c.SetReadDeadline(time.Now().Add(15 * time.Second)))
					var first, last environment.Stats
					for sample := range 3 {
						var message struct {
							Event string   `json:"event"`
							Args  []string `json:"args"`
						}
						for {
							must(c.ReadJSON(&message))
							if message.Event == "stats" {
								break
							}
						}
						usage, err := parseStreamedStats(message.Args, api.Utilization)
						must(err)
						must(updateStatsSample(sample, &first, &last, usage.Stats))
					}
					must(validateStatsAdvanced(first, last))
					h.add(map[string]any{"type": "check", "test": "automatic_stats_16_viewers", "variant": variant, "server_id": s.ID, "game": s.Kind, "viewer": viewer, "samples": 3, "first_uptime": first.Uptime, "last_uptime": last.Uptime, "last_stats": last, "pass": true})
				}()
			}
			wg.Wait()
			fmt.Printf("Validated %s %s automatic stats for 16 viewers\n", variant, s.Kind)
		}
	}
}
