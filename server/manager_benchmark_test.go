package server

import (
	"fmt"
	"testing"
)

func lookupServer(id string) *Server {
	return &Server{cfg: Configuration{configurationData: configurationData{Uuid: id}}}
}

// Last-entry and missing-ID requests exercise the scan's worst case. The
// entries have real configuration locks, but no Docker or game processes.
func BenchmarkManagerLookup(b *testing.B) {
	for _, count := range []int{1, 100, 1000} {
		for _, missing := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d/missing=%t", count, missing), func(b *testing.B) {
				manager := NewEmptyManager(nil)
				var id string
				for i := 0; i < count; i++ {
					id = fmt.Sprintf("%08x-1111-4111-8111-111111111111", i)
					manager.Add(lookupServer(id))
				}
				if missing {
					id = "ffffffff-1111-4111-8111-111111111111"
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					_, found := manager.Get(id)
					if found == missing {
						b.Fatal("incorrect lookup result")
					}
				}
			})
		}
	}
}
