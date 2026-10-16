package filesystem

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gabriel-vasile/mimetype"
	"golang.org/x/sys/unix"

	"github.com/pterodactyl/wings/config"
)

func newListingTestFS(t *testing.T) *Filesystem {
	t.Helper()
	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuthenticationToken = "test-token"
	config.Set(cfg)
	fs, err := New(t.TempDir(), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestDirectoryListingOrderAndFileKinds(t *testing.T) {
	fs := newListingTestFS(t)
	dirs := []string{"z_folder", "A folder"}
	files := []string{"z.txt", "a.txt", "<>&\"\n\u96ea.txt"}
	for _, name := range dirs {
		if err := os.Mkdir(filepath.Join(fs.Path(), name), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(fs.Path(), name), []byte("hello"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("a.txt", filepath.Join(fs.Path(), "link")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(fs.Path(), "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	files = append(files, "link", "pipe")
	slices.Sort(dirs)
	slices.Sort(files)
	want := append(dirs, files...)
	entries, err := fs.ListDirectory(".")
	if err != nil || len(entries) != len(want) {
		t.Fatalf("got %d entries: %v", len(entries), err)
	}
	for i := range entries {
		entry := &entries[i]
		if entry.Name() != want[i] {
			t.Fatalf("entry %d: got %q; want %q", i, entry.Name(), want[i])
		}
		if entry.IsDir() && entry.Mimetype != "inode/directory" {
			t.Fatalf("directory MIME: %q", entry.Mimetype)
		}
		if entry.Name() == "link" && !entry.ToAPIResponse().Symlink {
			t.Fatal("symlink metadata lost")
		}
		if (entry.Name() == "pipe" || entry.Name() == "link") && entry.Mimetype != "application/octet-stream" {
			t.Fatalf("special entry MIME: %q", entry.Mimetype)
		}
	}
}

func TestDirectoryListingDetectsExternalContentChanges(t *testing.T) {
	fs := newListingTestFS(t)
	path := filepath.Join(fs.Path(), "same.txt")
	for _, data := range [][]byte{[]byte("hello"), append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...), {}, []byte("hello again")} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		entries, err := fs.ListDirectory(".")
		if err != nil || len(entries) != 1 {
			t.Fatalf("entries: %v", err)
		}
		want, err := mimetype.DetectReader(bytes.NewReader(data))
		if err != nil || entries[0].Mimetype != want.String() || entries[0].Size() != int64(len(data)) {
			t.Fatalf("stale content metadata: %#v, %v", entries[0], err)
		}
	}
}
