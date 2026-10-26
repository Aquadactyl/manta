package tokens

import (
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"
	"github.com/stretchr/testify/assert"
)

func TestScoped(t *testing.T) {
	s := Scoped{Scope: "websocket file-upload"}
	assert.Equal(t, []string{"websocket", "file-upload"}, s.Scopes())
	assert.True(t, s.HasScope(Websocket))
	assert.True(t, s.HasScope(FileUpload))
	assert.False(t, s.HasScope(FileDownload))
}

func TestPayloadGetters(t *testing.T) {
	now := time.Now()
	jwtPayload := jwt.Payload{
		JWTID:    "jti-123",
		IssuedAt: jwt.NumericDate(now),
	}

	fp := FilePayload{
		Payload:    jwtPayload,
		ServerUuid: "srv-uuid",
		UserUuid:   "usr-uuid",
	}
	assert.Equal(t, "jti-123", fp.GetPayload().JWTID)

	bp := BackupPayload{
		Payload:    jwtPayload,
		ServerUuid: "srv-uuid",
		UserUuid:   "usr-uuid",
	}
	assert.Equal(t, "jti-123", bp.GetPayload().JWTID)

	up := UploadPayload{
		Payload:    jwtPayload,
		ServerUuid: "srv-uuid",
		UserUuid:   "usr-uuid",
	}
	assert.Equal(t, "jti-123", up.GetPayload().JWTID)

	wp := WebsocketPayload{
		Payload:     jwtPayload,
		ServerUUID:  "srv-uuid",
		UserUUID:    "usr-uuid",
		Permissions: []string{"control.start", "control.stop"},
	}
	assert.Equal(t, "jti-123", wp.GetPayload().JWTID)
	assert.Equal(t, "srv-uuid", wp.GetServerUuid())
}

func TestWebsocketPermissionsAndDenyJTI(t *testing.T) {
	bootTime := time.Now().Add(-1 * time.Hour)
	resetDenylistState(t, bootTime)

	wp := &WebsocketPayload{
		Payload: jwt.Payload{
			JWTID:    "ws-token-1",
			IssuedAt: jwt.NumericDate(time.Now()),
		},
		ServerUUID:  "srv-1",
		UserUUID:    "usr-1",
		Permissions: []string{"control.start", "*"},
	}

	assert.True(t, wp.HasPermission("control.start"))
	assert.True(t, wp.HasPermission("control.power"))
	assert.False(t, wp.HasPermission("admin.servers")) // Wildcard shouldn't grant admin permissions

	// Test DenyJTI
	DenyJTI("ws-token-1")
	time.Sleep(10 * time.Millisecond)
	// If issued before or at denial, HasPermission should return false because it's denylisted
	assert.False(t, wp.HasPermission("control.start"))

	// Non-matching permission
	wp2 := &WebsocketPayload{
		Payload: jwt.Payload{
			JWTID:    "ws-token-2",
			IssuedAt: jwt.NumericDate(time.Now()),
		},
		ServerUUID:  "srv-2",
		UserUUID:    "usr-2",
		Permissions: []string{"control.start"},
	}
	assert.False(t, wp2.HasPermission("control.stop"))
}
