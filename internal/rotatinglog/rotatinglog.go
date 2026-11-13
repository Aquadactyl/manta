// Package rotatinglog implements a log file that rotates itself by size.
//
// Platforms without logrotate, such as macOS, use it so the log file of the
// daemon does not grow without bound and nothing has to signal the process.
package rotatinglog

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// Writer appends to a file and, once the next write would take it past the
// maximum size, renames it to path.1, shifting the older files up by one and
// deleting the oldest. It is safe for concurrent use.
type Writer struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	keep    int
	file    logFile
	size    int64
}

type logFile interface {
	Write([]byte) (int, error)
	Stat() (os.FileInfo, error)
	Close() error
}

var openLogFile = func(path string, flag int, perm os.FileMode) (logFile, error) {
	return os.OpenFile(path, flag, perm) //nolint:gosec // path is the log file selected by the operator
}

var renameLogFile = os.Rename

// Open opens the file at path for appending, creating it if it is missing, and
// rotates it once it holds maxSize bytes, keeping up to keep old files. The size
// of an existing file counts, so restarting does not delay a rotation.
func Open(path string, maxSize int64, keep int) (*Writer, error) {
	if maxSize <= 0 {
		return nil, errors.New("rotatinglog: the maximum size must be positive")
	}
	if keep < 1 {
		return nil, errors.New("rotatinglog: at least one old file must be kept")
	}

	w := &Writer{path: path, maxSize: maxSize, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// open opens the file and records its current size.
func (w *Writer) open() error {
	f, err := openLogFile(w.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file, w.size = f, st.Size()
	return nil
}

// Write appends p to the file, rotating it first if it would grow too large. A
// single write is never split, so a file can exceed the maximum by one write
// when that write is larger than the maximum itself.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			// Keep logging to the file that is open rather than losing the line.
			// Rotation is attempted again on the next write.
			n, werr := w.write(p)
			return n, errors.Join(werr, err)
		}
	}
	return w.write(p)
}

func (w *Writer) write(p []byte) (int, error) {
	if w.file == nil {
		return 0, os.ErrClosed
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate moves the log file to path.1 and opens a new one. If the file can not
// be reopened the writer is left closed, so the error is returned to the caller.
func (w *Writer) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil

	// Make room by shifting path.N-1 to path.N, from the oldest to the newest.
	// The oldest file is overwritten by the rename.
	var rotateErr error
	for i := w.keep - 1; i >= 1; i-- {
		err := renameLogFile(w.numbered(i), w.numbered(i+1))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			rotateErr = err
			break
		}
	}
	if rotateErr == nil {
		if err := renameLogFile(w.path, w.numbered(1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			rotateErr = err
		}
	}

	// Always get a file back, even when renaming failed, so logging continues.
	if err := w.open(); err != nil {
		return errors.Join(rotateErr, err)
	}
	return rotateErr
}

func (w *Writer) numbered(i int) string {
	return fmt.Sprintf("%s.%d", w.path, i)
}

// Close closes the file. Writes after it fail with os.ErrClosed.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
