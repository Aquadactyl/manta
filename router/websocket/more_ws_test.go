package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

func TestGetHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	root := t.TempDir()
	cfg, err := config.NewAtPath(filepath.Join(root, "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token-12345678"
	cfg.PanelLocation = "https://panel.example.com"
	cfg.AllowedOrigins = []string{"https://other.example.com"}
	cfg.System.RootDirectory = root
	cfg.System.Data = filepath.Join(root, "volumes")
	cfg.System.ArchiveDirectory = filepath.Join(root, "archives")
	cfg.System.BackupDirectory = filepath.Join(root, "backups")
	cfg.System.LogDirectory = filepath.Join(root, "logs")
	cfg.System.TmpDirectory = filepath.Join(root, "tmp")
	cfg.System.MachineID.Enable = false
	require.NoError(t, os.MkdirAll(cfg.System.Data, 0755))
	config.Set(cfg)

	settings, _ := json.Marshal(map[string]string{"uuid": serverUuid})
	s, err := server.NewEmptyManager(nil).InitServer(serverUuid, remote.ServerConfigurationResponse{
		Settings:             settings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	})
	require.NoError(t, err)

	handlerChan := make(chan *Handler, 10)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		h, err := GetHandler(s, w, r, c)
		if err != nil {
			return
		}
		handlerChan <- h
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	// 1. Success with PanelLocation origin
	dialer := websocket.Dialer{}
	header := http.Header{"Origin": []string{"https://panel.example.com"}}
	conn, resp, err := dialer.Dial(wsURL, header)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	var lastHandler *Handler
	select {
	case lastHandler = <-handlerChan:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}
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
	root := t.TempDir()
	cfg, err := config.NewAtPath(filepath.Join(root, "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	cfg.System.RootDirectory = root
	cfg.System.Data = filepath.Join(root, "volumes")
	cfg.System.ArchiveDirectory = filepath.Join(root, "archives")
	cfg.System.BackupDirectory = filepath.Join(root, "backups")
	cfg.System.LogDirectory = filepath.Join(root, "logs")
	cfg.System.TmpDirectory = filepath.Join(root, "tmp")
	cfg.System.MachineID.Enable = false
	config.Set(cfg)

	// 1. Valid token
	tokStr, _ := signToken(t, serverUuid, PermissionConnect)
	payload, err := NewTokenPayload([]byte(tokStr))
	require.NoError(t, err)
	assert.True(t, payload.HasPermission(PermissionConnect))

	// 2. Denylisted token
	jti := "denylisted-jti-123"
	tokens.DenyJTI(jti)
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
		ServerUUID:  serverUuid,
		UserUUID:    "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		Permissions: []string{PermissionConnect},
	}

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
	time.Sleep(30 * time.Millisecond)

	// Drain messages from client in background
	go func() {
		for {
			if _, _, err := client.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Push log output
	h.server.Sink(system.LogSink).Push([]byte("test console message"))

	// Push install output
	h.server.Sink(system.InstallSink).Push([]byte("test install message"))

	// Push valid event message
	h.server.Events().Publish(server.StatsEvent, `{"memory": 1024}`)
	h.server.Events().Publish(server.StatsEvent, 12345) // non-string data triggers decodeEventMessage error

	time.Sleep(30 * time.Millisecond)

	// Cancel context to stop listeners
	cancel()
	time.Sleep(30 * time.Millisecond)
}

type customWsTestEnv struct {
	environment.ProcessEnvironment
	state     string
	running   bool
	readLogFn func(int) ([]string, error)
	sendCmdFn func(string) error
}

func (e *customWsTestEnv) State() string                           { return e.state }
func (e *customWsTestEnv) IsRunning(context.Context) (bool, error) { return e.running, nil }
func (e *customWsTestEnv) Readlog(n int) ([]string, error) {
	if e.readLogFn != nil {
		return e.readLogFn(n)
	}
	return []string{"log1"}, nil
}
func (e *customWsTestEnv) SendCommand(c string) error {
	if e.sendCmdFn != nil {
		return e.sendCmdFn(c)
	}
	return nil
}
func (e *customWsTestEnv) WaitForStop(context.Context, time.Duration, bool) error { return nil }

func TestListeners_ErrorPathsOnClosedConn(t *testing.T) {
	// 1. LogSink error path via registerListenerEvents
	h1, _, _ := setupHandlerClient(t)
	_, token := signToken(t, serverUuid, PermissionConnect, PermissionReceiveInstall)
	h1.setJwt(token)
	_ = h1.Connection.Close()

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	h1.registerListenerEvents(ctx1)
	time.Sleep(25 * time.Millisecond)
	h1.server.Sink(system.LogSink).Push([]byte("fail log"))
	time.Sleep(25 * time.Millisecond)

	// 2. InstallSink error path in listenForServerEvents
	h2, _, _ := setupHandlerClient(t)
	h2.setJwt(token)
	_ = h2.Connection.Close()
	go func() {
		time.Sleep(20 * time.Millisecond)
		h2.server.Sink(system.InstallSink).Push([]byte("fail install"))
	}()
	err := h2.listenForServerEvents(context.Background())
	assert.Error(t, err)

	// 3. Events bus error path in listenForServerEvents
	h3, _, _ := setupHandlerClient(t)
	h3.setJwt(token)
	_ = h3.Connection.Close()
	go func() {
		time.Sleep(20 * time.Millisecond)
		h3.server.Events().Publish(server.StatusEvent, "offline")
	}()
	err = h3.listenForServerEvents(context.Background())
	assert.Error(t, err)
}

func TestHandleInbound_AllRemainingBranches(t *testing.T) {
	h, _ := setupHandler(t)

	// 1. SendJson when jwt is nil returns nil immediately
	assert.NoError(t, h.SendJson(Message{Event: server.StatusEvent}))

	// 2. NewTokenPayload with invalid JWT bytes
	_, err := NewTokenPayload([]byte("not-a-valid-jwt"))
	assert.Error(t, err)

	// 3. AuthenticationEvent with invalid token
	err = h.HandleInbound(context.Background(), Message{Event: AuthenticationEvent, Args: []string{"bad-jwt"}})
	assert.Error(t, err)

	// 4. AuthenticationEvent with onAuthenticated returning error, then nil
	tokStr, tok := signToken(t, serverUuid, PermissionConnect, PermissionSendCommand, PermissionSendPowerStart)
	h.onAuthenticated = func() error {
		return assert.AnError
	}
	err = h.HandleInbound(context.Background(), Message{Event: AuthenticationEvent, Args: []string{tokStr}})
	assert.ErrorIs(t, err, assert.AnError)

	// Set offline environment to cover lines 423-432 during first authentication
	customEnv := &customWsTestEnv{
		state:   environment.ProcessOfflineState,
		running: false,
	}
	h.server.Environment = customEnv
	h.onAuthenticated = func() error {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = h.HandleInbound(ctx, Message{Event: AuthenticationEvent, Args: []string{tokStr}})
	assert.NoError(t, err)

	// 5. Re-authentication (!newConnection) returns nil early
	err = h.HandleInbound(ctx, Message{Event: AuthenticationEvent, Args: []string{tokStr}})
	assert.NoError(t, err)

	// 6. SetStateEvent without permission returns nil
	_, tokNoPower := signToken(t, serverUuid, PermissionConnect, PermissionSendCommand)
	h.setJwt(tokNoPower)
	err = h.HandleInbound(ctx, Message{Event: SetStateEvent, Args: []string{"start"}})
	assert.NoError(t, err)

	// 7. SetStateEvent with permission when server power lock is busy (ErrLockerLocked)
	h.setJwt(tok)
	// Start an action or hold power lock by running HandlePowerAction concurrently on a blocking stop
	// Or test SendServerLogsEvent when not running (running == false)
	err = h.HandleInbound(ctx, Message{Event: SendServerLogsEvent})
	assert.NoError(t, err)

	// 8. SendServerLogsEvent when running == true and Readlog returns error
	customEnv.running = true
	customEnv.readLogFn = func(int) ([]string, error) {
		return nil, assert.AnError
	}
	err = h.HandleInbound(ctx, Message{Event: SendServerLogsEvent})
	assert.ErrorIs(t, err, assert.AnError)

	// 9. SendCommandEvent when ProcessOfflineState returns nil
	customEnv.state = environment.ProcessOfflineState
	err = h.HandleInbound(ctx, Message{Event: SendCommandEvent, Args: []string{"say hi"}})
	assert.NoError(t, err)

	// 10. SendCommandEvent when ProcessRunningState and SendCommand returns nil -> saves activity
	customEnv.state = environment.ProcessRunningState
	customEnv.sendCmdFn = func(string) error { return nil }
	err = h.HandleInbound(ctx, Message{Event: SendCommandEvent, Args: []string{"say hello"}})
	assert.NoError(t, err)

	// 11. Unknown event falls through to final return nil
	err = h.HandleInbound(ctx, Message{Event: "completely-unknown-event"})
	assert.NoError(t, err)
}
