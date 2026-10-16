package tokens

import (
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

func TestParseTokenValidatesTimeClaims(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	now := time.Now()
	tests := []struct {
		name   string
		modify func(*jwt.Payload)
		err    error
	}{
		{"valid", func(*jwt.Payload) {}, nil},
		{"small clock difference", func(p *jwt.Payload) {
			p.IssuedAt = jwt.NumericDate(now.Add(2 * time.Second))
			p.NotBefore = jwt.NumericDate(now.Add(2 * time.Second))
		}, nil},
		{"missing expiry", func(p *jwt.Payload) { p.ExpirationTime = nil }, jwt.ErrExpValidation},
		{"expired", func(p *jwt.Payload) { p.ExpirationTime = jwt.NumericDate(now.Add(-time.Minute)) }, jwt.ErrExpValidation},
		{"expiry boundary", func(p *jwt.Payload) { p.ExpirationTime = jwt.NumericDate(now) }, jwt.ErrExpValidation},
		{"not yet valid", func(p *jwt.Payload) { p.NotBefore = jwt.NumericDate(now.Add(time.Minute)) }, jwt.ErrNbfValidation},
		{"future issuance", func(p *jwt.Payload) { p.IssuedAt = jwt.NumericDate(now.Add(time.Minute)) }, jwt.ErrIatValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := TransferPayload{Payload: jwt.Payload{
				IssuedAt:       jwt.NumericDate(now.Add(-time.Minute)),
				NotBefore:      jwt.NumericDate(now.Add(-time.Minute)),
				ExpirationTime: jwt.NumericDate(now.Add(time.Minute)),
			}}
			tt.modify(&p.Payload)
			token, err := jwt.Sign(&p, config.GetJwtAlgorithm())
			require.NoError(t, err)
			var parsed TransferPayload
			err = ParseToken(token, &parsed)
			if tt.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.err)
			}
		})
	}
}
