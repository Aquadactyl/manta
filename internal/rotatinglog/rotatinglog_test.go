package rotatinglog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wrappedLogFile struct {
	*os.File
	statErr  error
	closeErr error
}

func (f *wrappedLogFile) Stat() (os.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return f.File.Stat()
}

func (f *wrappedLogFile) Close() error {
	if f.closeErr != nil {
		return f.closeErr
	}
	return f.File.Close()
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return string(b)
}

func TestOpenValidatesArguments(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")

	_, err := Open(p, 0, 3)
	assert.Error(t, err)
	_, err = Open(p, 10, 0)
	assert.Error(t, err)
	_, err = Open(filepath.Join(t.TempDir(), "missing", "log"), 10, 3)
	assert.Error(t, err)
}

func TestOpenClosesFileWhenStatFails(t *testing.T) {
	statErr := errors.New("stat failed")
	originalOpen := openLogFile
	t.Cleanup(func() { openLogFile = originalOpen })
	var opened *os.File
	openLogFile = func(path string, flag int, perm os.FileMode) (logFile, error) {
		f, err := os.OpenFile(path, flag, perm)
		opened = f
		if err != nil {
			return nil, err
		}
		return &wrappedLogFile{File: f, statErr: statErr}, nil
	}

	_, err := Open(filepath.Join(t.TempDir(), "log"), 10, 1)
	assert.ErrorIs(t, err, statErr)
	_, err = opened.Write([]byte("x"))
	assert.ErrorIs(t, err, os.ErrClosed)
}

func TestWriteReturnsUnderlyingWriteError(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "log"), 10, 1)
	require.NoError(t, err)
	require.NoError(t, w.file.Close())

	n, err := w.Write([]byte("x"))
	assert.Zero(t, n)
	assert.ErrorIs(t, err, os.ErrClosed)
}

func TestRotationCloseErrorKeepsLogging(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	w, err := Open(p, 2, 1)
	require.NoError(t, err)
	require.NoError(t, w.file.Close())
	file, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	closeErr := errors.New("close failed")
	w.file = &wrappedLogFile{File: file, closeErr: closeErr}
	_, err = w.file.Write([]byte("a"))
	require.NoError(t, err)
	w.size = 1
	t.Cleanup(func() {
		w.file.(*wrappedLogFile).closeErr = nil
		_ = w.Close()
	})

	n, err := w.Write([]byte("bc"))
	assert.Equal(t, 2, n)
	assert.ErrorIs(t, err, closeErr)
	assert.Equal(t, "abc", read(t, p))
}

func TestRotationReopenFailureClosesWriter(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	reopenErr := errors.New("reopen failed")
	originalOpen := openLogFile
	t.Cleanup(func() { openLogFile = originalOpen })
	opens := 0
	openLogFile = func(path string, flag int, perm os.FileMode) (logFile, error) {
		opens++
		if opens == 2 {
			return nil, reopenErr
		}
		return os.OpenFile(path, flag, perm)
	}
	w, err := Open(p, 1, 1)
	require.NoError(t, err)
	_, err = w.Write([]byte("a"))
	require.NoError(t, err)

	n, err := w.Write([]byte("b"))
	assert.Zero(t, n)
	assert.ErrorIs(t, err, reopenErr)
	assert.ErrorIs(t, err, os.ErrClosed)
	assert.NoError(t, w.Close())
}

func TestRotationStopsWhenShiftingOldFileFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	shiftErr := errors.New("shift failed")
	originalRename := renameLogFile
	t.Cleanup(func() { renameLogFile = originalRename })
	renameLogFile = func(old, new string) error {
		if old == p+".1" && new == p+".2" {
			return shiftErr
		}
		return os.Rename(old, new)
	}
	w, err := Open(p, 1, 2)
	require.NoError(t, err)
	defer w.Close()
	_, err = w.Write([]byte("a"))
	require.NoError(t, err)

	n, err := w.Write([]byte("b"))
	assert.Equal(t, 1, n)
	assert.ErrorIs(t, err, shiftErr)
	assert.Equal(t, "ab", read(t, p))
}

func TestWriteRotatesAndShiftsOldFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wings.log")
	w, err := Open(p, 10, 3)
	require.NoError(t, err)
	defer w.Close()

	// Each line is 6 bytes, so two lines do not fit in 10 bytes.
	for i := 1; i <= 5; i++ {
		_, err := fmt.Fprintf(w, "line%d\n", i)
		require.NoError(t, err)
	}

	assert.Equal(t, "line5\n", read(t, p))
	assert.Equal(t, "line4\n", read(t, p+".1"))
	assert.Equal(t, "line3\n", read(t, p+".2"))
	assert.Equal(t, "line2\n", read(t, p+".3"))

	// Only three old files are kept, line1 was deleted.
	_, err = os.Stat(p + ".4")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestWriteDoesNotRotateBelowTheLimit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wings.log")
	w, err := Open(p, 100, 2)
	require.NoError(t, err)
	defer w.Close()

	for range 10 {
		_, err := w.Write([]byte("0123456789"))
		require.NoError(t, err)
	}

	assert.Len(t, read(t, p), 100)
	_, err = os.Stat(p + ".1")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestOversizedWriteIsNotSplit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wings.log")
	w, err := Open(p, 10, 2)
	require.NoError(t, err)
	defer w.Close()

	big := strings.Repeat("x", 50)
	_, err = w.Write([]byte("a"))
	require.NoError(t, err)
	n, err := w.Write([]byte(big))
	require.NoError(t, err)
	assert.Equal(t, 50, n)

	assert.Equal(t, big, read(t, p))
	assert.Equal(t, "a", read(t, p+".1"))
}

// The size of a file that already exists counts, so a restart does not let the
// log grow past the limit.
func TestOpenCountsTheSizeOfAnExistingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wings.log")
	require.NoError(t, os.WriteFile(p, []byte("12345678"), 0o600))

	w, err := Open(p, 10, 2)
	require.NoError(t, err)
	defer w.Close()

	_, err = w.Write([]byte("abc"))
	require.NoError(t, err)

	assert.Equal(t, "abc", read(t, p))
	assert.Equal(t, "12345678", read(t, p+".1"))
}

func TestFilesAreOwnerOnly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wings.log")
	w, err := Open(p, 10, 1)
	require.NoError(t, err)
	defer w.Close()

	_, err = w.Write([]byte("abcdefgh"))
	require.NoError(t, err)
	_, err = w.Write([]byte("abcdefgh"))
	require.NoError(t, err)

	for _, f := range []string{p, p + ".1"} {
		st, err := os.Stat(f)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm(), f)
	}
}

func TestWriteAfterCloseFails(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "wings.log"), 10, 1)
	require.NoError(t, err)

	require.NoError(t, w.Close())
	require.NoError(t, w.Close(), "closing twice is harmless")

	_, err = w.Write([]byte("x"))
	assert.ErrorIs(t, err, os.ErrClosed)
}

// If the file can not be moved, logging carries on in the file that is open
// instead of dropping the line, and the error is reported.
func TestRotationFailureKeepsLogging(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "wings.log")
	w, err := Open(p, 4, 1)
	require.NoError(t, err)
	defer w.Close()

	_, err = w.Write([]byte("abc"))
	require.NoError(t, err)

	// A file can not be renamed over a directory that is not empty.
	require.NoError(t, os.MkdirAll(filepath.Join(p+".1", "blocker"), 0o755))

	n, err := w.Write([]byte("def"))
	assert.Equal(t, 3, n)
	assert.Error(t, err)
	assert.Equal(t, "abcdef", read(t, p))

	// Once the obstacle is gone the next write rotates normally.
	require.NoError(t, os.RemoveAll(p+".1"))
	_, err = w.Write([]byte("ghi"))
	require.NoError(t, err)
	assert.Equal(t, "ghi", read(t, p))
	assert.Equal(t, "abcdef", read(t, p+".1"))
}

func TestConcurrentWritesLoseNothing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "wings.log")
	// Large enough that nothing is deleted, small enough to rotate often.
	w, err := Open(p, 512, 1000)
	require.NoError(t, err)

	const writers, lines = 8, 200
	var wg sync.WaitGroup
	for g := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range lines {
				_, err := fmt.Fprintf(w, "writer-%d-line-%03d\n", g, i)
				assert.NoError(t, err)
			}
		}()
	}
	wg.Wait()
	require.NoError(t, w.Close())

	files, err := filepath.Glob(p + "*")
	require.NoError(t, err)
	assert.Greater(t, len(files), 1, "the log should have rotated")

	total := 0
	for _, f := range files {
		total += strings.Count(read(t, f), "\n")
	}
	assert.Equal(t, writers*lines, total)
}
