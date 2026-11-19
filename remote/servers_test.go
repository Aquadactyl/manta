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

func TestGetServersUsesOneBasedPagination(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lastPage int
		empty    bool
	}{
		{"single page", 1, false},
		{"multiple pages", 7, false},
		{"empty node", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var requested []int
			c, srv := createTestClient(func(w http.ResponseWriter, r *http.Request) {
				page, err := strconv.Atoi(r.URL.Query().Get("page"))
				mu.Lock()
				requested = append(requested, page)
				mu.Unlock()
				// Match the panel's validation instead of normalizing page zero.
				if err != nil || page < 1 || page > tc.lastPage {
					http.Error(w, "The page must be between 1 and 2147483647.", http.StatusUnprocessableEntity)
					return
				}
				if r.URL.Path != "/servers" || r.URL.Query().Get("per_page") != "1" {
					http.Error(w, "unexpected server-list request", http.StatusBadRequest)
					return
				}
				data := []RawServerData{}
				if !tc.empty {
					data = append(data, RawServerData{UUID: strconv.Itoa(page)})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": data,
					"meta": Pagination{CurrentPage: uint(page), LastPage: uint(tc.lastPage), PerPage: 1},
				})
			})
			t.Cleanup(srv.Close)

			servers, err := c.GetServers(context.Background(), 1)
			require.NoError(t, err)
			mu.Lock()
			defer mu.Unlock()
			require.NotEmpty(t, requested)
			require.Equal(t, 1, requested[0])
			var expectedPages []int
			var expectedServers []string
			for page := 1; page <= tc.lastPage; page++ {
				expectedPages = append(expectedPages, page)
				if !tc.empty {
					expectedServers = append(expectedServers, strconv.Itoa(page))
				}
			}
			require.ElementsMatch(t, expectedPages, requested, "each page must be requested exactly once")
			var serverIDs []string
			for _, server := range servers {
				serverIDs = append(serverIDs, server.UUID)
			}
			require.ElementsMatch(t, expectedServers, serverIDs)
		})
	}
}

func TestGetServersBoundsPaginationConcurrency(t *testing.T) {
	var active, peak atomic.Int32
	firstWave := make(chan struct{})
	var firstWaveOnce sync.Once
	release := make(chan struct{})
	c, srv := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page < 1 {
			http.Error(w, "invalid page", http.StatusUnprocessableEntity)
			return
		}
		if page > 1 {
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
			"data": []RawServerData{{UUID: strconv.Itoa(page)}},
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
		seen[s.UUID] = true
	}
	require.Len(t, seen, 12)
}

func TestGetServersStopsSchedulingAfterFailure(t *testing.T) {
	var requests atomic.Int32
	c, srv := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("page") == "1" {
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
