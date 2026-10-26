package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

func TestGetHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token-12345678"
	cfg.PanelLocation = "https://panel.example.com"
	cfg.AllowedOrigins = []string{"https://other.example.com"}
	config.Set(cfg)

	settings, _ := json.Marshal(map[string]string{"uuid": serverUuid})
	s, err := server.NewEmptyManager(nil).InitServer(serverUuid, remote.ServerConfigurationResponse{
		Settings:             settings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	})
	require.NoError(t, err)

	var lastHandler *Handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		h, err := GetHandler(s, w, r, c)
		if err != nil {
			return
		}
		lastHandler = h
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	// 1. Success with PanelLocation origin
	dialer := websocket.Dialer{}
	header := http.Header{"Origin": []string{"https://panel.example.com"}}
	conn, resp, err := dialer.Dial(wsURL, header)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	assert.NotNil(t, lastHandler)
	assert.NotEmpty(t, lastHandler.Uuid().String())
	assert.Equal(t, s, lastHandler.server)
	_ = conn.Close()

	// 2. Success with AllowedOrigins
	header = http.Header{"Origin": []string{"https://other.example.com"}}
	conn, resp, err = dialer.Dial(wsURL, header)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	_ = conn.Close()

	// 3. Rejection with disallowed origin
	header = http.Header{"Origin": []string{"https://malicious.example.com"}}
	conn, _, err = dialer.Dial(wsURL, header)
	assert.Error(t, err)
	if conn != nil {
		_ = conn.Close()
	}
}

func TestNewTokenPayload_DenylistAndPermissions(t *testing.T) {
	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	config.Set(cfg)

	// 1. Valid token
	tokStr, _ := signToken(t, serverUuid, PermissionConnect)
	payload, err := NewTokenPayload([]byte(tokStr))
	require.NoError(t, err)
	assert.True(t, payload.HasPermission(PermissionConnect))

	// 2. Denylisted token
	func() {
		defer func() { _ = recover() }()
		_ = database.Initialize()
	}()
	jti := "denylisted-jti-123"
	now := time.Now()
	customPayload := &tokens.WebsocketPayload{
		Payload: jwt.Payload{
			Issuer:         "pterodactyl",
			Subject:        "websocket",
			Audience:       jwt.Audience{"pterodactyl"},
			ExpirationTime: jwt.NumericDate(now.Add(time.Hour)),
			NotBefore:      jwt.NumericDate(now.Add(-time.Hour)),
			IssuedAt:       jwt.NumericDate(now.Add(-time.Hour)),
			JWTID:          jti,
		},
		Scoped:      tokens.Scoped{Scope: string(tokens.Websocket)},
		Permissions: []string{PermissionConnect},
	}
	_ = database.Instance().Exec("INSERT INTO denylist (jti, created_at) VALUES (?, ?)", jti, time.Now()).Error

	denylistedBytes, err := jwt.Sign(customPayload, config.GetJwtAlgorithm())
	require.NoError(t, err)
	_, err = NewTokenPayload(denylistedBytes)
	assert.ErrorIs(t, err, ErrJwtOnDenylist)

	// 3. Missing connect permission
	noPermPayload := &tokens.WebsocketPayload{
		Payload: jwt.Payload{
			ExpirationTime: jwt.NumericDate(now.Add(time.Hour)),
			NotBefore:      jwt.NumericDate(now.Add(-time.Minute)),
			IssuedAt:       jwt.NumericDate(now.Add(time.Second * 2)),
			JWTID:          "no-perm-jti",
		},
		Scoped:      tokens.Scoped{Scope: string(tokens.Websocket)},
		ServerUUID:  serverUuid,
		UserUUID:    "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		Permissions: []string{PermissionSendCommand},
	}
	noPermBytes, err := jwt.Sign(noPermPayload, config.GetJwtAlgorithm())
	require.NoError(t, err)
	_, err = NewTokenPayload(noPermBytes)
	assert.ErrorIs(t, err, ErrJwtNoConnectPerm)
}

func TestListeners_LogAndInstallAndExpiration(t *testing.T) {
	h, _, client := setupHandlerClient(t)
	_, token := signToken(t, serverUuid, PermissionConnect, PermissionReceiveInstall)
	h.setJwt(token)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Test registerListenerEvents
	h.registerListenerEvents(ctx)

	// Push log output
	h.server.Sink(system.LogSink).Push([]byte("test console message"))

	// Push install output
	h.server.Sink(system.InstallSink).Push([]byte("test install message"))

	// Push event message
	h.server.Events().Publish(server.StatsEvent, `{"memory": 1024}`)

	// Read messages on client
	_ = client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var msg Message
	_ = client.ReadJSON(&msg)

	// Cancel context to stop listeners
	cancel()
	time.Sleep(50 * time.Millisecond)
}
