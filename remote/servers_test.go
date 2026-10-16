package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetServersBoundsPaginationConcurrency(t *testing.T) {
	var active, peak atomic.Int32
	firstWave := make(chan struct{})
	var firstWaveOnce sync.Once
	release := make(chan struct{})
	c, srv := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		} else {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			if n == 4 {
				firstWaveOnce.Do(func() { close(firstWave) })
			}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []RawServerData{{Uuid: strconv.Itoa(page)}},
			"meta": Pagination{CurrentPage: uint(page), LastPage: 12},
		})
	})
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	var servers []RawServerData
	var err error
	go func() {
		defer close(done)
		servers, err = c.GetServers(ctx, 50)
	}()
	select {
	case <-firstWave:
	case <-ctx.Done():
		close(release)
		t.Fatal("pagination workers did not start")
	}
	// Keep the first wave busy long enough to observe an accidental fifth
	// simultaneous request before allowing the rest of the pages to complete.
	time.Sleep(50 * time.Millisecond)
	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("pagination did not complete")
	}
	require.NoError(t, err)
	require.Len(t, servers, 12)
	require.Equal(t, int32(4), peak.Load())
	seen := make(map[string]bool)
	for _, s := range servers {
		seen[s.Uuid] = true
	}
	require.Len(t, seen, 12)
}

func TestGetServersStopsSchedulingAfterFailure(t *testing.T) {
	var requests atomic.Int32
	c, srv := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("page") == "0" {
			_, _ = w.Write([]byte(`{"data":[],"meta":{"current_page":1,"last_page":1000000}}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})
	t.Cleanup(srv.Close)
	_, err := c.GetServers(context.Background(), 50)
	require.Error(t, err)
	require.LessOrEqual(t, requests.Load(), int32(6))
}
