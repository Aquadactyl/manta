package docker

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/pterodactyl/wings/environment"
)

// loopStatsReader models a persistent Docker stream without creating a new
// decoder or buffering a large number of repeated frames for each sample.
type loopStatsReader struct {
	frame  []byte
	offset int
}

func (r *loopStatsReader) Read(p []byte) (int, error) {
	n := copy(p, r.frame[r.offset:])
	r.offset = (r.offset + n) % len(r.frame)
	return n, nil
}

var decodedStatsSink environment.Stats

func BenchmarkDockerStatsStream(b *testing.B) {
	frame, err := os.ReadFile("testdata/stats-v2.json")
	if err != nil {
		b.Fatal(err)
	}
	dec := json.NewDecoder(&loopStatsReader{frame: frame})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		decodedStatsSink, err = decodeResourceStats(dec, 1000)
		if err != nil {
			b.Fatal(err)
		}
	}
}
