package tokens

import (
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
)

type TokenStore struct {
	sync.Mutex
	cache *cache.Cache
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
			cache: cache.New(time.Minute*60, time.Minute*5),
		}
	})

	return _tokens
}

// Checks if a token is valid or not.
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

	_, exists := t.cache.Get(token)

	if !exists {
		t.cache.Set(token, "", ttl)
	}

	return !exists
}
