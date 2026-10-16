package tokens

import (
	"time"

	"github.com/gbrlsnchs/jwt/v3"

	"github.com/pterodactyl/wings/config"
)

type TokenData interface {
	GetPayload() *jwt.Payload
}

// Allow small differences between the Panel and node clocks for issuance and
// not-before claims. Expiry remains strict, including at the expiry boundary.
const tokenClockSkew = 30 * time.Second

// Validates the provided JWT against the known secret for the Daemon and returns the
// parsed data. This function DOES NOT validate that the token is valid for the connected
// server, nor does it ensure that the user providing the token is able to actually do things.
//
// This simply returns a parsed token.
func ParseToken(token []byte, data TokenData) error {
	now := time.Now()
	verifyOptions := jwt.ValidatePayload(
		data.GetPayload(),
		func(p *jwt.Payload) error {
			if p.ExpirationTime == nil || !now.Before(p.ExpirationTime.Time) {
				return jwt.ErrExpValidation
			}
			return nil
		},
		jwt.NotBeforeValidator(now.Add(tokenClockSkew)),
		jwt.IssuedAtValidator(now.Add(tokenClockSkew)),
	)

	_, err := jwt.Verify(token, config.GetJwtAlgorithm(), &data, verifyOptions)

	return err
}
