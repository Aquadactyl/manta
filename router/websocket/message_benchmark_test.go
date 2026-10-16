package websocket

import (
	"encoding/json"
	"fmt"
	"testing"
)

// This is the bus envelope and resource payload shape produced by Server.Proc.
// Values are synthetic; both versions receive identical bytes.
var benchmarkStatsEvent = []byte(`{"Topic":"stats","Data":{"memory_bytes":624234496,"memory_limit_bytes":2147483648,"cpu_absolute":12.763,"network":{"rx_bytes":381249,"tx_bytes":927156},"uptime":7365124,"state":"running","disk_bytes":372941576}}`)

var benchmarkEventWire []byte

// Measure event conversion plus the outgoing JSON encoding for one event
// delivered independently to each viewer. Excludes sockets, JWTs and bus waits.
func BenchmarkEventMessage(b *testing.B) {
	for _, viewers := range []int{1, 16, 64} {
		b.Run(fmt.Sprintf("stats/viewers_%d", viewers), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for range viewers {
					message, err := decodeEventMessage(benchmarkStatsEvent)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkEventWire, err = json.Marshal(message)
					if err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
	b.Run("status", func(b *testing.B) {
		data := []byte(`{"Topic":"status","Data":"running"}`)
		b.ReportAllocs()
		for b.Loop() {
			message, err := decodeEventMessage(data)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkEventWire, err = json.Marshal(message)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
