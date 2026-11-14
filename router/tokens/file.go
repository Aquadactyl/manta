package tokens

import (
	"github.com/gbrlsnchs/jwt/v3"
)

type FilePayload struct {
	jwt.Payload
	Scoped

	FilePath   string `json:"file_path"`
	ServerUUID string `json:"server_uuid"`
	UserUUID   string `json:"user_uuid"`
	UniqueID   string `json:"unique_id"`
}

// GetPayload returns the JWT payload.
func (p *FilePayload) GetPayload() *jwt.Payload {
	return &p.Payload
}

// IsUniqueRequest reports whether this JWT is valid for the request cycle. If the
// unique ID passed in the token has already been seen before this will
// return false. This allows us to use this JWT as a one-time token that
// validates all of the request.
func (p *FilePayload) IsUniqueRequest() bool {
	return p.ExpirationTime != nil && getTokenStore().IsValidUntil(p.UniqueID, p.ExpirationTime.Time)
}

// Denylisted returns true if this token was issued before the user's access to
// the server was revoked.
func (p *FilePayload) Denylisted() bool {
	return isDenylisted(&p.Payload, p.ServerUUID, p.UserUUID)
}
