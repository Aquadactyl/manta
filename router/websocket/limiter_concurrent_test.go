package websocket

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestConcurrentMessagesShareTheSameRateLimit(t *testing.T) {
	for range 20 {
		bucket := NewLimiter()
		start := make(chan struct{})
		results := make(chan *rate.Limiter, 64)
		var wg sync.WaitGroup
		for i := range 64 {
			wg.Go(func() {
				<-start
				event := SendStatsEvent
				if i%2 == 0 {
					event = SetStateEvent
				}
				results <- bucket.For(event)
			})
		}
		close(start)
		wg.Wait()
		close(results)
		shared := bucket.For(SendStatsEvent)
		for limiter := range results {
			require.Same(t, shared, limiter)
		}
	}
}

func TestUnknownEventNamesDoNotGrowLimiterState(t *testing.T) {
	h, _ := setupHandler(t)
	for i := range 10000 {
		h.IsThrottled(Event("unrecognized-event-" + strconv.Itoa(i)))
	}
	require.Len(t, h.limiter.limits, 1)
	require.Len(t, h.limiter.throttles, 1)
	require.False(t, h.IsThrottled(SendCommandEvent))
	require.False(t, h.IsThrottled(AuthenticationEvent))
	require.Len(t, h.limiter.throttles, 3)
}
