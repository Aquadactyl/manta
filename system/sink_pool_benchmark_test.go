package system

import (
	"fmt"
	"testing"
)

// Ready receivers model the normal console/event path without backpressure.
// Draining each queue keeps the same delivery work in both implementations.
func BenchmarkSinkPoolReady(b *testing.B) {
	for _, count := range []int{0, 1, 16, 64} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			pool := NewSinkPool()
			channels := make([]chan []byte, count)
			for i := range channels {
				channels[i] = make(chan []byte, 1)
				pool.On(channels[i])
			}
			defer pool.Destroy()
			payload := []byte("benchmark console message")
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				pool.Push(payload)
				for _, ch := range channels {
					<-ch
				}
			}
		})
	}
}
