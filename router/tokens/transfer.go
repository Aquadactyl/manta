package tokens

import (
	"github.com/golang-jwt/jwt/v5"
)

type TransferPayload struct {
	jwt.RegisteredClaims
	Scoped
}
