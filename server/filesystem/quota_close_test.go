package filesystem

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/ufs"
)

type quotaStatFailure struct {
	ufs.File
	err    error
	closed bool
}

func (f *quotaStatFailure) Stat() (ufs.FileInfo, error) { return nil, f.err }
func (f *quotaStatFailure) Close() error                { f.closed = true; return errors.New("close failed") }

func TestQuotaCloseStillClosesFileAfterStatFailure(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	fs, err := New(t.TempDir(), 100, nil)
	require.NoError(t, err)
	defer fs.UnixFS().Close()
	fs.unixFS.SetUsage(10)
	statErr := errors.New("stat failed")
	underlying := &quotaStatFailure{err: statErr}
	f := newQuotaFile(fs, underlying, 10)
	require.ErrorIs(t, f.Close(), statErr)
	require.True(t, underlying.closed)
	require.Equal(t, int64(10), fs.unixFS.Usage(), "failed stat must not alter usage")
}
