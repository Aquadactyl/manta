package tokens

import (
	"container/heap"
	"sync"
	"time"
)

type TokenStore struct {
	sync.Mutex
	tokens map[string]time.Time
	expiry tokenExpiryHeap
}

type tokenExpiry struct {
	token string
	at    time.Time
}

type tokenExpiryHeap []tokenExpiry

func (h tokenExpiryHeap) Len() int           { return len(h) }
func (h tokenExpiryHeap) Less(i, j int) bool { return h[i].at.Before(h[j].at) }
func (h tokenExpiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *tokenExpiryHeap) Push(x any)        { *h = append(*h, x.(tokenExpiry)) }
func (h *tokenExpiryHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

var (
	_tokens     *TokenStore
	_tokensOnce sync.Once
)

// Returns the global unique token store cache. This is used to validate
// one time token usage by storing any received tokens in a local memory
// cache until they are ready to expire.
func getTokenStore() *TokenStore {
	_tokensOnce.Do(func() {
		_tokens = &TokenStore{
			tokens: make(map[string]time.Time),
		}
	})

	return _tokens
}

// IsValidToken reports whether a token is valid.
func (t *TokenStore) IsValidToken(token string) bool {
	return t.IsValidUntil(token, time.Now().Add(time.Hour))
}

// IsValidUntil consumes a token once and retains it until its signed expiry.
// A fixed cache lifetime would allow a longer-lived token to be replayed.
func (t *TokenStore) IsValidUntil(token string, expiresAt time.Time) bool {
	t.Lock()
	defer t.Unlock()
	ttl := time.Until(expiresAt)
	if token == "" || ttl <= 0 {
		return false
	}
	if t.tokens == nil {
		t.tokens = make(map[string]time.Time)
	}
	for len(t.expiry) > 0 && !t.expiry[0].at.After(time.Now()) {
		entry := heap.Pop(&t.expiry).(tokenExpiry)
		if t.tokens[entry.token].Equal(entry.at) {
			delete(t.tokens, entry.token)
		}
	}
	if expires, exists := t.tokens[token]; exists && expires.After(time.Now()) {
		return false
	}
	t.tokens[token] = expiresAt
	heap.Push(&t.expiry, tokenExpiry{token: token, at: expiresAt})
	return true
}
