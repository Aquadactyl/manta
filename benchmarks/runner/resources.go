package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

type snapshot struct {
	CPU     uint64
	Working uint64
	PIDs    uint64
}

func (h *harness) stats(container string) snapshot {
	var data struct {
		CPUStats struct {
			CPUUsage struct {
				Total uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
		} `json:"cpu_stats"`
		MemoryStats struct {
			Usage uint64            `json:"usage"`
			Stats map[string]uint64 `json:"stats"`
		} `json:"memory_stats"`
		PidsStats struct {
			Current uint64 `json:"current"`
		} `json:"pids_stats"`
	}
	must(json.Unmarshal(h.dk("/containers/"+container+"/stats?stream=false&one-shot=true"), &data))
	working := data.MemoryStats.Usage
	cache := data.MemoryStats.Stats["inactive_file"]
	if cache < working {
		working -= cache
	}
	return snapshot{data.CPUStats.CPUUsage.Total, working, data.PidsStats.Current}
}
func (h *harness) resources() {
	h.startAll()
	time.Sleep(5 * time.Second)
	for _, load := range []bool{false, true} {
		label := "idle"
		if load {
			label = "command_load"
		}
		stop := make(chan struct{})
		var wg sync.WaitGroup
		if load {
			for _, variant := range []string{"manta", "wings"} {
				n := h.Nodes[variant]
				p := h.find(n, "paper")
				b := h.find(n, "bedrock")
				h.commands(n, p, "forceload add -32 -32 32 32", "gamerule randomTickSpeed 0")
				h.commands(n, b, "tickingarea add circle 8 64 8 2 benchmark true")
				wg.Add(1)
				go func() {
					defer wg.Done()
					ticker := time.NewTicker(200 * time.Millisecond)
					defer ticker.Stop()
					i := 0
					for {
						select {
						case <-stop:
							return
						case <-ticker.C:
							block := "stone"
							if i%2 == 1 {
								block = "air"
							}
							h.commands(n, p, "fill 0 64 0 15 79 15 "+block+" replace")
							h.commands(n, b, "fill 0 64 0 15 79 15 "+block+" replace")
							i++
						}
					}
				}()
			}
		}
		containers := map[string]map[string]string{}
		before := map[string]snapshot{}
		samples := map[string][]float64{}
		peaks := map[string]uint64{}
		for k, n := range h.Nodes {
			containers[k] = map[string]string{"daemon": n.Container}
			for _, s := range n.Servers {
				containers[k][s.Kind] = s.UUID
			}
			for _, id := range containers[k] {
				before[id] = h.stats(id)
			}
		}
		started := time.Now()
		for i := 0; i < 15; i++ {
			for _, group := range containers {
				for _, id := range group {
					st := h.stats(id)
					samples[id] = append(samples[id], float64(st.Working)/(1<<20))
					if st.Working > peaks[id] {
						peaks[id] = st.Working
					}
				}
			}
			time.Sleep(2 * time.Second)
		}
		elapsed := time.Since(started)
		for variant, group := range containers {
			for role, id := range group {
				after := h.stats(id)
				cpu := float64(after.CPU-before[id].CPU) / 1e9
				sum := 0.0
				for _, v := range samples[id] {
					sum += v
				}
				h.add(map[string]any{"type": "measurement", "test": "resources_" + label, "variant": variant, "role": role, "duration_s": elapsed.Seconds(), "cpu_core_seconds": cpu, "cpu_core_percent": cpu / elapsed.Seconds() * 100, "mean_working_mib": sum / float64(len(samples[id])), "peak_working_mib": float64(peaks[id]) / (1 << 20), "pids": after.PIDs})
			}
		}
		for variant, n := range h.Nodes {
			for _, s := range n.Servers {
				lat := []float64{}
				var status string
				for i := 0; i < 50; i++ {
					start := time.Now()
					var err error
					if s.Kind == "paper" {
						status, err = javaPing(s.UUID, s.Port)
					} else {
						status, err = bedrockPing(s.UUID, s.Port)
					}
					if err != nil {
						panic(fmt.Sprintf("%s game query: %v", s.Name, err))
					}
					lat = append(lat, float64(time.Since(start).Microseconds())/1000)
				}
				row := distribution(lat)
				row["type"] = "measurement"
				row["test"] = "game_query_during_" + label
				row["variant"] = variant
				row["game"] = s.Kind
				row["status"] = status
				h.add(row)
			}
			p := h.find(n, "paper")
			h.commands(n, p, "tps", "mspt")
			time.Sleep(500 * time.Millisecond)
			raw := string(h.api(n, "GET", "/api/servers/"+p.UUID+"/logs?size=100", nil))
			re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
			h.add(map[string]any{"type": "game_console", "test": "paper_ticks_" + label, "variant": variant, "logs": re.ReplaceAllString(raw, "")})
		}
		close(stop)
		wg.Wait()
		if load {
			for _, n := range h.Nodes {
				h.commands(n, h.find(n, "paper"), "forceload remove all")
				h.commands(n, h.find(n, "bedrock"), "tickingarea remove benchmark")
			}
		}
	}
}
func varint(v int32) []byte {
	b := []byte{}
	u := uint32(v)
	for {
		c := byte(u & 127)
		u >>= 7
		if u != 0 {
			c |= 128
		}
		b = append(b, c)
		if u == 0 {
			return b
		}
	}
}
func readVar(r io.Reader) (int, error) {
	value := 0
	for i := 0; i < 5; i++ {
		b := []byte{0}
		if _, err := io.ReadFull(r, b); err != nil {
			return 0, err
		}
		value |= int(b[0]&127) << (7 * i)
		if b[0]&128 == 0 {
			return value, nil
		}
	}
	return 0, fmt.Errorf("bad varint")
}
func javaPing(host string, port int) (string, error) {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), 3*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	must(c.SetDeadline(time.Now().Add(5 * time.Second)))
	b := append([]byte{0}, varint(-1)...)
	b = append(b, varint(int32(len(host)))...)
	b = append(b, host...)
	b = binary.BigEndian.AppendUint16(b, uint16(port))
	b = append(b, 1)
	b = append(varint(int32(len(b))), b...)
	b = append(b, 1, 0)
	if _, err = c.Write(b); err != nil {
		return "", err
	}
	n, err := readVar(c)
	if err != nil {
		return "", err
	}
	if n < 2 || n > 1<<20 {
		return "", fmt.Errorf("bad packet length")
	}
	p := make([]byte, n)
	if _, err = io.ReadFull(c, p); err != nil {
		return "", err
	}
	offset := 1
	for p[offset]&128 != 0 {
		offset++
	}
	offset++
	var data struct {
		Version struct {
			Name string `json:"name"`
		}
	}
	err = json.Unmarshal(p[offset:], &data)
	return data.Version.Name, err
}
func bedrockPing(host string, port int) (string, error) {
	c, err := net.DialTimeout("udp", net.JoinHostPort(host, fmt.Sprint(port)), 3*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	must(c.SetDeadline(time.Now().Add(3 * time.Second)))
	b := []byte{1}
	b = binary.BigEndian.AppendUint64(b, uint64(time.Now().UnixMilli()))
	b = append(b, []byte{0, 255, 255, 0, 254, 254, 254, 254, 253, 253, 253, 253, 18, 52, 86, 120}...)
	b = binary.BigEndian.AppendUint64(b, 42)
	if _, err = c.Write(b); err != nil {
		return "", err
	}
	out := make([]byte, 2048)
	n, err := c.Read(out)
	if err != nil {
		return "", err
	}
	if n < 35 || out[0] != 0x1c {
		return "", fmt.Errorf("bad RakNet pong")
	}
	status := strings.Split(string(out[35:n]), ";")
	if len(status) > 3 {
		return status[3], nil
	}
	return string(out[35:n]), nil
}
