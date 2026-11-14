package tokens

import (
	"github.com/golang-jwt/jwt/v5"
)

type BackupPayload struct {
	jwt.RegisteredClaims
	Scoped

	ServerUUID string `json:"server_uuid"`
	UserUUID   string `json:"user_uuid"`
	BackupUUID string `json:"backup_uuid"`
	UniqueID   string `json:"unique_id"`
}

// IsUniqueRequest reports whether this JWT is valid for the request cycle. If the
// unique ID passed in the token has already been seen before this will
// return false. This allows us to use this JWT as a one-time token that
// validates all of the request.
func (p *BackupPayload) IsUniqueRequest() bool {
	return p.ExpiresAt != nil && getTokenStore().IsValidUntil(p.UniqueID, p.ExpiresAt.Time)
}

// Denylisted returns true if this token was issued before the user's access to
// the server was revoked.
func (p *BackupPayload) Denylisted() bool {
	return isDenylisted(&p.RegisteredClaims, p.ServerUUID, p.UserUUID)
}
