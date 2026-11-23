package filesystem

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Aquadactyl/manta/config"
)

func newArchiveBenchmarkFS(b *testing.B) *Filesystem {
	b.Helper()
	cfg, err := config.NewAtPath(filepath.Join(b.TempDir(), "config.yml"))
	if err != nil {
		b.Fatal(err)
	}
	cfg.AuthenticationToken = "benchmark-token"
	config.Set(cfg)
	fsys, err := New(b.TempDir(), 0, nil)
	if err != nil {
		b.Fatal(err)
	}
	return fsys
}

func BenchmarkArchiveSelection(b *testing.B) {
	for _, count := range []int{1, 1000} {
		b.Run(fmt.Sprintf("selected_%d/filter_miss", count), func(b *testing.B) {
			fsys := newArchiveBenchmarkFS(b)
			defer fsys.Close()
			if err := os.WriteFile(filepath.Join(fsys.Path(), "unselected"), nil, 0o600); err != nil {
				b.Fatal(err)
			}
			entries, err := fsys.ReadDir(".")
			if err != nil {
				b.Fatal(err)
			}
			files := make([]string, count)
			for i := range files {
				files[i] = fmt.Sprintf("plugins/selected-%d/config.yml", i)
			}
			a := &Archive{Filesystem: fsys, Files: files}
			callback := a.withFilesCallback()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := callback("unselected", "plugins/other/config.yml", entries[0]); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkArchiveSelectedStream(b *testing.B) {
	fsys := newArchiveBenchmarkFS(b)
	defer fsys.Close()
	for i := range 1000 {
		if err := os.WriteFile(filepath.Join(fsys.Path(), fmt.Sprintf("file-%04d", i)), []byte("data"), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	for _, count := range []int{1, 1000} {
		b.Run(fmt.Sprintf("selected_%d", count), func(b *testing.B) {
			files := make([]string, count)
			for i := range files {
				files[i] = fmt.Sprintf("missing-%04d", i)
			}
			files[len(files)-1] = "file-0999"
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
