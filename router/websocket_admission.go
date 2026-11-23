package router

import "sync"

// Bound upgraded connections which have not authenticated yet. Authenticated
// sockets retain the existing per-server limit; pending clients cannot consume
// those slots. Entries disappear when their last pending connection is released.
type websocketAdmission struct {
	mu       sync.Mutex
	total    int
	byIP     map[string]int
	maxTotal int
	maxPerIP int
}

func (a *websocketAdmission) acquire(ip string) (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.total >= a.maxTotal || a.byIP[ip] >= a.maxPerIP {
		return nil, false
	}
	if a.byIP == nil {
		a.byIP = make(map[string]int)
	}
	a.total++
	a.byIP[ip]++
	return sync.OnceFunc(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.total--
		if a.byIP[ip] <= 1 {
			delete(a.byIP, ip)
		} else {
			a.byIP[ip]--
		}
	}), true
}
