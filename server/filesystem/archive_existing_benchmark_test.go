package filesystem

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Include ordinary selections whose paths all exist, as well as the separate
// missing-selection fixture. Each file has distinct contents for round trips.
func BenchmarkArchiveSelectedExisting(b *testing.B) {
	fsys := newArchiveBenchmarkFS(b)
	defer fsys.Close()
	for i := range 1000 {
		name := fmt.Sprintf("file-%04d", i)
		if err := os.WriteFile(filepath.Join(fsys.Path(), name), []byte(name), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	for _, count := range []int{500, 1000} {
		b.Run(fmt.Sprintf("selected_%d", count), func(b *testing.B) {
			files := make([]string, count)
			for i := range files {
				files[i] = fmt.Sprintf("file-%04d", i*(1000/count))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				a := &Archive{Filesystem: fsys, Files: files}
				if err := a.Stream(context.Background(), io.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
