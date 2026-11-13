package system

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTryAcquireDestroyRaceDrainsPendingToken(t *testing.T) {
	l := NewLocker()
	l.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- l.TryAcquire(context.Background()) }()

	deadline := time.Now().Add(time.Second)
	for len(l.ch) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	require.Equal(t, 1, len(l.ch), "TryAcquire did not enqueue its token")
	close(l.done)
	<-l.ch
	l.mu.Unlock()

	require.ErrorIs(t, <-done, ErrLockerDestroyed)
	require.False(t, l.IsLocked())
}
