package system

import (
	"sync"
	"testing"
)

func TestSinkPoolHasListenersAcrossLifecycle(t *testing.T) {
	p := NewSinkPool()
	if p.HasListeners() {
		t.Fatal("empty pool reports a listener")
	}
	a, b := make(chan []byte, 1), make(chan []byte, 1)
	p.On(a)
	p.On(b)
	if !p.HasListeners() {
		t.Fatal("registered listeners were missed")
	}
	p.Off(a)
	if !p.HasListeners() {
		t.Fatal("removing one listener hid the remaining one")
	}
	p.Off(b)
	if p.HasListeners() {
		t.Fatal("last listener was not removed")
	}
	p.On(make(chan []byte, 1))
	p.Destroy()
	if p.HasListeners() {
		t.Fatal("destroyed pool reports a listener")
	}
}

func TestSinkPoolHasListenersDuringSubscriptionChanges(t *testing.T) {
	p := NewSinkPool()
	defer p.Destroy()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 1000 {
				c := make(chan []byte, 1)
				p.On(c)
				if !p.HasListeners() {
					t.Error("missed a listener before its removal")
				}
				p.Off(c)
				_ = p.HasListeners()
			}
		})
	}
	wg.Wait()
	if p.HasListeners() {
		t.Fatal("listeners remain after every subscriber left")
	}
}
