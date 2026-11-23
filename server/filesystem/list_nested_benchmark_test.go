package filesystem

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Aquadactyl/manta/config"
)

// Also exercise real plugin-folder paths and distinct file contents, rather
// than relying only on the small identical root files in the original fixture.
func BenchmarkDirectoryListingNested(b *testing.B) {
	benchmarkListingPath(b, "plugins/example/config", false)
}

func BenchmarkDirectoryListingUnique(b *testing.B) {
	benchmarkListingPath(b, ".", true)
}

func benchmarkListingPath(b *testing.B, directory string, unique bool) {
	cfg, err := config.NewAtPath(filepath.Join(b.TempDir(), "config.yml"))
	if err != nil {
		b.Fatal(err)
	}
	cfg.AuthenticationToken = "benchmark-token"
	config.Set(cfg)
	root := b.TempDir()
	dir := filepath.Join(root, directory)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatal(err)
	}
	for i := range 1000 {
		data := bytes.Repeat([]byte("x"), 256)
		if unique {
			copy(data, fmt.Sprintf("configuration %04d ", i))
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.txt", i)), data, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	fs, err := New(root, 0, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := fs.Close(); err != nil {
			b.Error(err)
		}
	})
	for _, encode := range []bool{false, true} {
		name := "read"
		if encode {
			name = "api_json"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				entries, err := fs.ListDirectory(directory)
				if err != nil || len(entries) != 1000 {
					b.Fatalf("got %d entries: %v", len(entries), err)
				}
				if encode {
					data := make([]StatResponse, len(entries))
					for i := range entries {
						data[i] = entries[i].ToAPIResponse()
					}
					if _, err := json.Marshal(data); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
