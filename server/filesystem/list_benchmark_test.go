package filesystem

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pterodactyl/wings/config"
)

// Exercise real directory reads, containment, metadata and content detection.
// The fixtures use the same size/text content as the local live API workload.
func BenchmarkDirectoryListing(b *testing.B) {
	for _, count := range []int{10, 1000} {
		b.Run(fmt.Sprintf("entries_%d", count), func(b *testing.B) {
			cfg, err := config.NewAtPath(filepath.Join(b.TempDir(), "config.yml"))
			if err != nil {
				b.Fatal(err)
			}
			cfg.AuthenticationToken = "benchmark-token"
			config.Set(cfg)
			root := b.TempDir()
			for i := range count {
				if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%04d.txt", i)), bytes.Repeat([]byte("x"), 256), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			fs, err := New(root, 0, nil)
			if err != nil {
				b.Fatal(err)
			}
			for _, encode := range []bool{false, true} {
				name := "read"
				if encode {
					name = "read_and_json"
				}
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						entries, err := fs.ListDirectory(".")
						if err != nil || len(entries) != count {
							b.Fatalf("got %d entries: %v", len(entries), err)
						}
						if encode {
							if _, err := json.Marshal(entries); err != nil {
								b.Fatal(err)
							}
						}
					}
				})
			}
		})
	}
}
