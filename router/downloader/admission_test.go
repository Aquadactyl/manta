package downloader

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Aquadactyl/manta/server"
)

func TestConcurrentDownloadsReserveOnlyTheAllowedSlots(t *testing.T) {
	s, err := server.New(nil)
	require.NoError(t, err)
	s.Config().UUID = uuid.NewString()
	start := make(chan struct{})
	accepted := make(chan *Download, 100)
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			<-start
			if dl, ok := NewWithLimit(s, DownloadRequest{}, 3); ok {
				accepted <- dl
			}
		})
	}
	close(start)
	wg.Wait()
	close(accepted)
	var downloads []*Download
	for dl := range accepted {
		downloads = append(downloads, dl)
		t.Cleanup(dl.Cancel)
	}
	require.Len(t, downloads, 3)
	require.Len(t, ByServer(s.ID()), 3)
	downloads[0].Cancel()
	next, ok := NewWithLimit(s, DownloadRequest{}, 3)
	require.True(t, ok, "cancellation must release its slot")
	t.Cleanup(next.Cancel)
	_, ok = NewWithLimit(s, DownloadRequest{}, 3)
	require.False(t, ok)
	other, err := server.New(nil)
	require.NoError(t, err)
	other.Config().UUID = uuid.NewString()
	dl, ok := NewWithLimit(other, DownloadRequest{}, 3)
	require.True(t, ok, "another server must have independent capacity")
	t.Cleanup(dl.Cancel)
}
