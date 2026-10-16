package tokens

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

// A token is accepted only once.
func TestTokenIsAcceptedOnce(t *testing.T) {
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if getTokenStore().IsValidToken("token-used-at-once") {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), accepted.Load())
}

func TestTokenStoreRetainsTokensUntilSignedExpiry(t *testing.T) {
	store := &TokenStore{cache: cache.New(time.Hour, 0)}
	expiresAt := time.Now().Add(3 * time.Hour)
	require.True(t, store.IsValidUntil("long-lived-token", expiresAt))
	_, cachedExpiry, found := store.cache.GetWithExpiration("long-lived-token")
	require.True(t, found)
	require.WithinDuration(t, expiresAt, cachedExpiry, time.Second)
	require.False(t, store.IsValidUntil("long-lived-token", expiresAt))
	require.False(t, store.IsValidUntil("", expiresAt))
	require.False(t, store.IsValidUntil("expired", time.Now().Add(-time.Second)))
}

func TestOneTimePayloadsRequireIDAndExpiry(t *testing.T) {
	for name, payload := range map[string]interface{ IsUniqueRequest() bool }{
		"file": &FilePayload{}, "backup": &BackupPayload{}, "upload": &UploadPayload{},
	} {
		t.Run(name+" missing expiry", func(t *testing.T) { require.False(t, payload.IsUniqueRequest()) })
	}
	p := jwt.Payload{ExpirationTime: jwt.NumericDate(time.Now().Add(time.Minute))}
	for name, payload := range map[string]interface{ IsUniqueRequest() bool }{
		"file": &FilePayload{Payload: p}, "backup": &BackupPayload{Payload: p}, "upload": &UploadPayload{Payload: p},
	} {
		t.Run(name+" missing ID", func(t *testing.T) { require.False(t, payload.IsUniqueRequest()) })
	}
}
