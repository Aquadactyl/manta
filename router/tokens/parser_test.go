package tokens

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/Aquadactyl/manta/config"
)

func TestParseTokenValidatesTimeClaims(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	now := time.Now()
	tests := []struct {
		name   string
		modify func(*jwt.RegisteredClaims)
		err    error
	}{
		{"valid", func(*jwt.RegisteredClaims) {}, nil},
		{"small clock difference", func(p *jwt.RegisteredClaims) {
			p.IssuedAt = jwt.NewNumericDate(now.Add(2 * time.Second))
			p.NotBefore = jwt.NewNumericDate(now.Add(2 * time.Second))
		}, nil},
		{"missing expiry", func(p *jwt.RegisteredClaims) { p.ExpiresAt = nil }, jwt.ErrTokenRequiredClaimMissing},
		{"expired", func(p *jwt.RegisteredClaims) { p.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Minute)) }, jwt.ErrTokenExpired},
		{"expiry boundary", func(p *jwt.RegisteredClaims) { p.ExpiresAt = jwt.NewNumericDate(now) }, jwt.ErrTokenExpired},
		{"not yet valid", func(p *jwt.RegisteredClaims) { p.NotBefore = jwt.NewNumericDate(now.Add(time.Minute)) }, jwt.ErrTokenNotValidYet},
		{"future issuance", func(p *jwt.RegisteredClaims) { p.IssuedAt = jwt.NewNumericDate(now.Add(time.Minute)) }, jwt.ErrTokenUsedBeforeIssued},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := TransferPayload{RegisteredClaims: jwt.RegisteredClaims{
				IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
				NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			}}
			tt.modify(&p.RegisteredClaims)
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &p).SignedString(config.GetJWTKey())
			require.NoError(t, err)
			var parsed TransferPayload
			err = ParseToken([]byte(token), &parsed)
			if tt.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.err)
			}
		})
	}
}
