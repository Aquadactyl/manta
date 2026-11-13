//go:build unix

package ufs_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pterodactyl/wings/internal/ufs"
)

// A directory that is removed after it has been listed, but before the walk
// opens it, is reported to the callback rather than ending the walk.
func TestWalkDiratReportsDirectoriesRemovedDuringWalk(t *testing.T) {
	t.Parallel()

	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Cleanup()

	for _, dir := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(fs.Root, dir, "child"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	dirfd, name, closeFd, err := fs.SafePath("/")
	defer closeFd()
	if err != nil {
		t.Fatal(err)
	}

	var reported []string
	var visited []string
	err = fs.WalkDirat(dirfd, name, func(_ int, _, relative string, d ufs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, ufs.ErrNotExist) {
				reported = append(reported, relative)
				return nil
			}
			return err
		}
		visited = append(visited, relative)
		// Remove the directory after it has been listed and before it is opened.
		if relative == "b" {
			return os.RemoveAll(filepath.Join(fs.Root, "b"))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected the walk to continue past a removed directory, got %v", err)
	}
	if len(reported) != 1 || reported[0] != "b" {
		t.Fatalf("expected the removed directory to be reported, got %v", reported)
	}
	for _, want := range []string{"a/child", "c/child"} {
		found := false
		for _, v := range visited {
			found = found || v == want
		}
		if !found {
			t.Fatalf("expected %s to be visited, visited %v", want, visited)
		}
	}
}

func TestWalkDiratSkipDirAndCallbackErrors(t *testing.T) {
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Cleanup()
	for _, p := range []string{"skip/hidden", "visit/file"} {
		if err := os.MkdirAll(filepath.Join(fs.Root, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if filepath.Ext(p) != "" {
			if err := os.WriteFile(filepath.Join(fs.Root, p), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	dirfd, _, closeFd, err := fs.SafePath("/")
	if err != nil {
		t.Fatal(err)
	}
	defer closeFd()

	var skipCalled bool
	err = fs.WalkDirat(dirfd, "skip", func(_ int, _, _ string, d ufs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		skipCalled = true
		if d == nil || !d.IsDir() {
			t.Fatal("skip target should be a directory")
		}
		return ufs.SkipDir
	})
	if err != nil {
		t.Fatalf("WalkDirat SkipDir: %v", err)
	}
	if !skipCalled {
		t.Fatal("WalkDirat did not invoke callback for SkipDir target")
	}

	want := errors.New("callback failed")
	err = fs.WalkDirat(dirfd, "visit", func(_ int, _, _ string, _ ufs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("WalkDirat error = %v, want %v", err, want)
	}
}
