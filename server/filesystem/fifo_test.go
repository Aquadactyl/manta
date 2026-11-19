package filesystem

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Named pipes can be inspected without blocking, and write operations reject them.
func TestNamedPipesAreNeverOpened(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })
	fs.SetDiskLimit(1024 * 1024 * 1024)

	ops := []struct {
		name      string
		wantError bool
		op        func(p string) error
	}{
		{"File", false, func(p string) error {
			f, st, err := fs.File(p)
			if err != nil {
				return err
			}
			defer f.Close()
			if st.Mode()&os.ModeNamedPipe == 0 {
				return errors.New("file is not a named pipe")
			}
			return nil
		}},
		{"Touch", true, func(p string) error {
			f, err := fs.Touch(p, os.O_RDWR)
			if err == nil {
				_ = f.Close()
			}
			return err
		}},
		{"Write", true, func(p string) error {
			b := make([]byte, 1<<20)
			return fs.Write(p, bytes.NewReader(b), int64(len(b)), 0o644)
		}},
		{"Copy", true, func(p string) error { return fs.Copy(p) }},
		{"ReadDir", true, func(p string) error {
			_, err := fs.ReadDir(p)
			return err
		}},
		{"DecompressFile", true, func(p string) error { return fs.DecompressFile(context.Background(), "/", p) }},
		{"SpaceAvailableForDecompression", true, func(p string) error {
			return fs.SpaceAvailableForDecompression(context.Background(), "/", p)
		}},
	}

	for _, tt := range ops {
		t.Run(tt.name, func(t *testing.T) {
			// Opening a pipe for writing unblocks anything waiting to read from it,
			// so each operation gets a pipe of its own.
			p := "pipe-" + tt.name
			if err := unix.Mkfifo(filepath.Join(rfs.root, "server", p), 0o644); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- tt.op(p) }()
			select {
			case err := <-done:
				if tt.wantError && err == nil {
					t.Fatal("expected an error for a named pipe")
				}
				if !tt.wantError && err != nil {
					t.Fatalf("unexpected error inspecting named pipe: %v", err)
				}
			case <-time.After(time.Second * 2):
				t.Fatal("blocked on a named pipe")
			}
		})
	}
}
