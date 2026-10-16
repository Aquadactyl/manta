package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/pterodactyl/wings/environment"
)

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
			if len(api.Utilization) != 7 {
				panic("unexpected API resource schema")
			}
			var wg sync.WaitGroup
			for viewer := range 16 {
				c, event := h.socket(n, s, h.token(n, s, "websocket", nil))
				if event != "auth success" {
					_ = c.Close()
					panic("event stream authentication failed")
				}
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
						if len(message.Args) != 1 {
							panic("stats event must carry one string argument")
						}
						data := []byte(message.Args[0])
						var keys map[string]json.RawMessage
						must(json.Unmarshal(data, &keys))
						if len(keys) != len(api.Utilization) {
							panic("stats event schema differs from API")
						}
						for key := range api.Utilization {
							if _, ok := keys[key]; !ok {
								panic("stats event is missing resource field " + key)
							}
						}
						var usage struct {
							environment.Stats
							State string `json:"state"`
							Disk  int64  `json:"disk_bytes"`
						}
						must(json.Unmarshal(data, &usage))
						if usage.State != "running" || usage.Memory == 0 || usage.MemoryLimit < usage.Memory || usage.Uptime <= 0 || usage.Disk <= 0 || math.IsNaN(usage.CpuAbsolute) || math.IsInf(usage.CpuAbsolute, 0) || usage.CpuAbsolute < 0 {
							panic(fmt.Sprintf("invalid streamed resource values for %s %s server %d: state=%s memory=%d limit=%d uptime=%d disk=%d cpu=%v", variant, s.Kind, s.ID, usage.State, usage.Memory, usage.MemoryLimit, usage.Uptime, usage.Disk, usage.CpuAbsolute))
						}
						if sample == 0 {
							first = usage.Stats
						} else if usage.Uptime < last.Uptime || usage.Network.RxBytes < last.Network.RxBytes || usage.Network.TxBytes < last.Network.TxBytes {
							panic("streamed resource counters regressed")
						}
						last = usage.Stats
					}
					if last.Uptime <= first.Uptime {
						panic("streamed uptime did not advance")
					}
					h.add(map[string]any{"type": "check", "test": "automatic_stats_16_viewers", "variant": variant, "server_id": s.ID, "game": s.Kind, "viewer": viewer, "samples": 3, "first_uptime": first.Uptime, "last_uptime": last.Uptime, "last_stats": last, "pass": true})
				}()
			}
			wg.Wait()
			fmt.Printf("Validated %s %s automatic stats for 16 viewers\n", variant, s.Kind)
		}
	}
}
