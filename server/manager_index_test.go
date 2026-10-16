package server

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagerIndexTracksMutationsAndDuplicateOrder(t *testing.T) {
	var manager Manager // The zero value is usable too.
	first, duplicate, other := lookupServer("same"), lookupServer("same"), lookupServer("other")
	get := func(id string, expected *Server) {
		t.Helper()
		got, ok := manager.Get(id)
		require.Equal(t, expected != nil, ok)
		require.Same(t, expected, got)
	}
	get("same", nil)
	manager.Add(first)
	manager.Add(duplicate)
	manager.Add(other)
	get("same", first)
	require.False(t, manager.AddIfMissing(lookupServer("same")))
	require.Equal(t, []string{"same", "same", "other"}, manager.Keys())
	manager.Remove(func(s *Server) bool { return s == first })
	get("same", duplicate)
	manager.Remove(func(s *Server) bool { return s.ID() == "same" })
	get("same", nil)
	require.True(t, manager.AddIfMissing(first))
	get("same", first)
	manager.Put([]*Server{duplicate, first})
	get("same", duplicate)
	get("other", nil)
	manager.Put(nil)
	get("same", nil)
	require.Zero(t, manager.Len())
}

func TestManagerOwnsCollectionSnapshots(t *testing.T) {
	manager := NewEmptyManager(nil)
	s := lookupServer("server")
	input := []*Server{s}
	manager.Put(input)
	input[0] = nil
	all := manager.All()
	require.Same(t, s, all[0])
	all[0] = nil
	require.Equal(t, []string{"server"}, manager.Keys())
	got, ok := manager.Get("server")
	require.True(t, ok)
	require.Same(t, s, got)
}

func TestManagerConcurrentLookupAndMutation(t *testing.T) {
	manager := NewEmptyManager(nil)
	stable := lookupServer("stable")
	manager.Add(stable)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 300 {
				got, ok := manager.Get("stable")
				if !ok || got != stable {
					t.Error("stable server lost during concurrent mutation")
				}
				for _, s := range manager.All() {
					if s == nil {
						t.Error("nil server in collection snapshot")
					}
				}
				manager.Get("missing")
			}
		})
	}
	for i := range 100 {
		id := fmt.Sprint(i)
		manager.AddIfMissing(lookupServer(id))
		manager.Remove(func(s *Server) bool { return s.ID() == id })
		manager.Put([]*Server{stable})
	}
	wg.Wait()
	require.Equal(t, []*Server{stable}, manager.All())
}
