package filesystem

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestArchiveStreamReturnsIgnoreBudgetError(t *testing.T) {
	fs, err := New(t.TempDir(), 0, nil)
	require.NoError(t, err)
	fs.SetTest(true)
	require.NoError(t, fs.Writefile("file.txt", strings.NewReader("contents")))
	t.Cleanup(func() { _ = fs.Close() })

	want := errors.New("ignore budget exceeded")
	previous := trackIgnoreMatch
	trackIgnoreMatch = func(*ignoreMatchBudget, time.Duration) error { return want }
	t.Cleanup(func() { trackIgnoreMatch = previous })

	archive := &Archive{Filesystem: fs, BaseDirectory: "/", Ignore: "*.ignored"}
	require.ErrorIs(t, archive.Stream(context.Background(), io.Discard), want)
}

func TestArchiveStreamReturnsWalkEntryError(t *testing.T) {
	fs, err := New(t.TempDir(), 0, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = fs.Close() })

	archive := &Archive{Filesystem: fs, BaseDirectory: "missing"}
	require.ErrorIs(t, archive.Stream(context.Background(), io.Discard), os.ErrNotExist)
}
