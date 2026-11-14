package tokens

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	store := &TokenStore{}
	expiresAt := time.Now().Add(3 * time.Hour)
	require.True(t, store.IsValidUntil("long-lived-token", expiresAt))
	require.WithinDuration(t, expiresAt, store.tokens["long-lived-token"], time.Second)
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
	p := jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}
	for name, payload := range map[string]interface{ IsUniqueRequest() bool }{
		"file": &FilePayload{RegisteredClaims: p}, "backup": &BackupPayload{RegisteredClaims: p}, "upload": &UploadPayload{RegisteredClaims: p},
	} {
		t.Run(name+" missing ID", func(t *testing.T) { require.False(t, payload.IsUniqueRequest()) })
	}
}
