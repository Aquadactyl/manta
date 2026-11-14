package tokens

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Aquadactyl/manta/config"
)

// Allow small differences between the Panel and node clocks for issuance and
// not-before claims. Expiry remains strict, including at the expiry boundary.
const tokenClockSkew = 30 * time.Second

// ParseToken validates the JWT against the Daemon secret and returns the
// parsed data. This function DOES NOT validate that the token is valid for the connected
// server, nor does it ensure that the user providing the token is able to actually do things.
//
// This simply returns a parsed token.
func ParseToken(token []byte, claims jwt.Claims) error {
	now := time.Now()
	parsed, err := jwt.ParseWithClaims(string(token), claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected JWT signing method %q", token.Method.Alg())
		}
		return config.GetJWTKey(), nil
	}, jwt.WithoutClaimsValidation())
	if err != nil {
		return err
	}
	if parsed == nil || !parsed.Valid {
		return jwt.ErrTokenInvalidClaims
	}
	expiresAt, _ := claims.GetExpirationTime()
	if expiresAt == nil {
		return jwt.ErrTokenRequiredClaimMissing
	} else if !now.Before(expiresAt.Time) {
		return jwt.ErrTokenExpired
	}
	notBefore, _ := claims.GetNotBefore()
	if notBefore != nil && now.Add(tokenClockSkew).Before(notBefore.Time) {
		return jwt.ErrTokenNotValidYet
	}
	issuedAt, _ := claims.GetIssuedAt()
	if issuedAt != nil && now.Add(tokenClockSkew).Before(issuedAt.Time) {
		return jwt.ErrTokenUsedBeforeIssued
	}
	return nil
}
