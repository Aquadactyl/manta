package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"
	"github.com/gorilla/websocket"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/server"
)

const (
	serverUuid      = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	otherServerUuid = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

var errCommandSent = errors.New("command sent")

// commandEnvironment is a running environment that records the commands sent
// to it.
type commandEnvironment struct {
	environment.ProcessEnvironment
	commands atomic.Int64
}

func (e *commandEnvironment) State() string {
	return environment.ProcessRunningState
}

func (e *commandEnvironment) IsRunning(context.Context) (bool, error) {
	return true, nil
}

func (e *commandEnvironment) Readlog(int) ([]string, error) {
	return []string{"server console line"}, nil
}

func (e *commandEnvironment) WaitForStop(context.Context, time.Duration, bool) error {
	return nil
}

func (e *commandEnvironment) SendCommand(string) error {
	e.commands.Add(1)
	// Returning an error skips saving the activity, which needs a database.
	return errCommandSent
}

func setupHandler(t *testing.T) (*Handler, *commandEnvironment) {
	t.Helper()
	h, env, client := setupHandlerClient(t)
	go func() {
		for {
			if _, _, err := client.ReadMessage(); err != nil {
				return
			}
		}
	}()
	return h, env
}

func setupHandlerClient(t *testing.T) (*Handler, *commandEnvironment, *websocket.Conn) {
	t.Helper()

	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuthenticationToken = "test-token"
	config.Set(cfg)

	settings, _ := json.Marshal(map[string]string{"uuid": serverUuid})
	s, err := server.NewEmptyManager(nil).InitServer(serverUuid, remote.ServerConfigurationResponse{
		Settings:             settings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := &commandEnvironment{}
	s.Environment = env

	conns := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		conns <- c
	}))
	t.Cleanup(srv.Close)

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	h := &Handler{Connection: <-conns, server: s, limiter: NewLimiter()}
	t.Cleanup(func() { _ = h.Connection.Close() })
	return h, env, client
}

func signToken(t *testing.T, forServer string, permissions ...string) (string, *tokens.WebsocketPayload) {
	t.Helper()
	return signTokenFor(t, forServer, "cccccccc-cccc-4ccc-8ccc-cccccccccccc", permissions...)
}

func signTokenFor(t *testing.T, forServer string, userUuid string, permissions ...string) (string, *tokens.WebsocketPayload) {
	t.Helper()
	// Tokens issued before Wings booted are rejected, and the issued time only has
	// second precision.
	now := time.Now().Add(time.Second * 2)
	payload := tokens.WebsocketPayload{
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			ExpirationTime: jwt.NumericDate(now.Add(time.Minute * 10)),
		},
		Scoped:      tokens.Scoped{Scope: string(tokens.Websocket)},
		UserUUID:    userUuid,
		ServerUUID:  forServer,
		Permissions: permissions,
	}
	b, err := jwt.Sign(&payload, config.GetJwtAlgorithm())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := NewTokenPayload(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), parsed
}

// Activity is attributed to the user whose token authorized it, even if another
// user's token is on the socket since.
func TestActivityIsAttributedToTheAuthorizingToken(t *testing.T) {
	h, _ := setupHandler(t)
	_, authorizing := signTokenFor(t, serverUuid, "aaaaaaaa-0000-4000-8000-000000000001", PermissionConnect, PermissionSendCommand)
	_, other := signTokenFor(t, serverUuid, "bbbbbbbb-0000-4000-8000-000000000002", PermissionConnect)
	h.setJwt(other)

	a := h.activity(authorizing).Event(server.ActivityConsoleCommand, nil)
	if a.User.String != authorizing.UserUUID {
		t.Fatalf("expected activity to be attributed to %s, got %q", authorizing.UserUUID, a.User.String)
	}
}

func TestAuthenticationRejectsTokenForDifferentServer(t *testing.T) {
	h, _ := setupHandler(t)
	_, current := signToken(t, serverUuid, PermissionConnect)
	other, _ := signToken(t, otherServerUuid, PermissionConnect, PermissionSendCommand)
	h.setJwt(current)

	err := h.HandleInbound(context.Background(), Message{Event: AuthenticationEvent, Args: []string{other}})
	if !errors.Is(err, ErrJwtUuidMismatch) {
		t.Fatalf("expected a token for a different server to be rejected, got %v", err)
	}
	if h.GetJwt() != current {
		t.Fatal("expected the token on the socket to be unchanged")
	}
}

// Messages on a socket are handled concurrently, and commands are only ever
// authorized by a token for the server the socket belongs to.
func TestCommandIsNotAuthorizedByTokenForDifferentServer(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("the messages cannot be handled concurrently with a single CPU")
	}
	h, env := setupHandler(t)
	_, current := signToken(t, serverUuid, PermissionConnect)
	other, _ := signToken(t, otherServerUuid, PermissionConnect, PermissionSendCommand)

	deadline := time.Now().Add(time.Second * 5)
	for i := 0; i < 20000 && time.Now().Before(deadline); i++ {
		h.setJwt(current)
		h.limiter = NewLimiter()

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = h.HandleInbound(context.Background(), Message{Event: AuthenticationEvent, Args: []string{other}})
		}()
		// Vary the order the two messages are handled in.
		for n := 0; n < i%64; n++ {
			_ = n * n
		}
		_ = h.HandleInbound(context.Background(), Message{Event: SendCommandEvent, Args: []string{"say hello"}})
		wg.Wait()

		if n := env.commands.Load(); n > 0 {
			t.Fatalf("expected the command to be refused (iteration %d, %d commands)", i, n)
		}
	}
}

func TestIsJwtError(t *testing.T) {
	if !IsJwtError(ErrJwtNotPresent) {
		t.Error("expected ErrJwtNotPresent to be jwt error")
	}
	if !IsJwtError(ErrJwtNoConnectPerm) {
		t.Error("expected ErrJwtNoConnectPerm to be jwt error")
	}
	if !IsJwtError(ErrJwtUuidMismatch) {
		t.Error("expected ErrJwtUuidMismatch to be jwt error")
	}
	if !IsJwtError(ErrJwtOnDenylist) {
		t.Error("expected ErrJwtOnDenylist to be jwt error")
	}
	if !IsJwtError(jwt.ErrExpValidation) {
		t.Error("expected jwt.ErrExpValidation to be jwt error")
	}
	if IsJwtError(errors.New("generic error")) {
		t.Error("expected generic error not to be jwt error")
	}
}

func TestHandlerMethods(t *testing.T) {
	h, _, client := setupHandlerClient(t)

	// Uuid & Logger
	if h.Uuid().String() == "" {
		t.Error("expected non-empty uuid")
	}
	if h.Logger() == nil {
		t.Error("expected non-nil logger")
	}

	// User when not authenticated
	if h.User() != "" {
		t.Errorf("expected empty user, got %s", h.User())
	}

	// TokenValid when not authenticated
	if !errors.Is(h.TokenValid(), ErrJwtNotPresent) {
		t.Errorf("expected ErrJwtNotPresent, got %v", h.TokenValid())
	}

	// Authenticate
	tok, payload := signToken(t, serverUuid, PermissionConnect, PermissionSendCommand)
	if err := h.HandleInbound(context.Background(), Message{Event: AuthenticationEvent, Args: []string{tok}}); err != nil {
		t.Fatal(err)
	}

	if h.User() != payload.UserUUID {
		t.Errorf("expected %s, got %s", payload.UserUUID, h.User())
	}
	if err := h.TokenValid(); err != nil {
		t.Errorf("expected valid token, got %v", err)
	}

	// SendThrottled
	if err := h.SendThrottled("command"); err != nil {
		t.Errorf("SendThrottled: %v", err)
	}

	// SendErrorJson
	if err := h.SendErrorJson(Message{Event: SendCommandEvent}, errors.New("fail")); err != nil {
		t.Errorf("SendErrorJson: %v", err)
	}
	if err := h.SendErrorJson(Message{Event: SendCommandEvent}, ErrJwtNoConnectPerm); err != nil {
		t.Errorf("SendErrorJson JWT: %v", err)
	}

	// OnAuthenticated callback
	called := false
	h.OnAuthenticated(func() error {
		called = true
		return nil
	})
	if err := h.onAuthenticated(); err != nil || !called {
		t.Error("expected onAuthenticated to be called")
	}

	// SendClose
	_ = client.Close()
}

func TestHandlerInboundEvents(t *testing.T) {
	func() { defer func() { _ = recover() }(); _ = database.Initialize() }()
	h, env, _ := setupHandlerClient(t)

	// 1. Inbound event before auth -> sends JWT error
	err := h.HandleInbound(context.Background(), Message{Event: SendStatsEvent})
	if err != nil {
		t.Errorf("expected nil from unauthenticated event, got %v", err)
	}

	// 2. Authenticate with mismatched server UUID
	tokBadServer, _ := signToken(t, otherServerUuid, PermissionConnect)
	err = h.HandleInbound(context.Background(), Message{Event: AuthenticationEvent, Args: []string{tokBadServer}})
	if !errors.Is(err, ErrJwtUuidMismatch) {
		t.Errorf("expected ErrJwtUuidMismatch, got %v", err)
	}

	// 3. Authenticate successfully
	tok, _ := signToken(t, serverUuid, PermissionConnect, PermissionSendCommand, PermissionSendPowerStart, PermissionSendPowerStop)
	err = h.HandleInbound(context.Background(), Message{Event: AuthenticationEvent, Args: []string{tok}})
	if err != nil {
		t.Fatalf("auth failed: %v", err)
	}

	// Re-authenticate (refreshing token)
	err = h.HandleInbound(context.Background(), Message{Event: AuthenticationEvent, Args: []string{tok}})
	if err != nil {
		t.Fatalf("re-auth failed: %v", err)
	}

	// 4. SendStatsEvent
	err = h.HandleInbound(context.Background(), Message{Event: SendStatsEvent})
	if err != nil {
		t.Errorf("SendStatsEvent: %v", err)
	}

	// 5. SendServerLogsEvent
	err = h.HandleInbound(context.Background(), Message{Event: SendServerLogsEvent})
	if err != nil {
		t.Errorf("SendServerLogsEvent: %v", err)
	}

	// 6. SendCommandEvent
	err = h.HandleInbound(context.Background(), Message{Event: SendCommandEvent, Args: []string{"say hello"}})
	if !errors.Is(err, errCommandSent) {
		t.Errorf("expected errCommandSent, got %v", err)
	}
	if env.commands.Load() != 1 {
		t.Errorf("expected 1 command, got %d", env.commands.Load())
	}

	// 7. SetStateEvent
	_ = h.HandleInbound(context.Background(), Message{Event: SetStateEvent, Args: []string{"start"}})
	_ = h.HandleInbound(context.Background(), Message{Event: SetStateEvent, Args: []string{"stop"}})

	// 8. Suspended server
	h.server.Config().SetSuspended(true)
	err = h.HandleInbound(context.Background(), Message{Event: SendStatsEvent})
	if !errors.Is(err, server.ErrSuspended) {
		t.Errorf("expected ErrSuspended, got %v", err)
	}
	h.server.Config().SetSuspended(false)

	// 9. SetStateEvent without permission
	_, pNoPower := signToken(t, serverUuid, PermissionConnect)
	h.setJwt(pNoPower)
	err = h.HandleInbound(context.Background(), Message{Event: SetStateEvent, Args: []string{"start"}})
	if err != nil {
		t.Errorf("expected nil for unauthorized power event, got %v", err)
	}

	_, pFull := signToken(t, serverUuid, PermissionConnect, PermissionSendCommand, PermissionSendPowerStart, PermissionSendPowerStop)
	h.setJwt(pFull)

	// 10. SendCommandEvent without permission
	h.setJwt(pNoPower)
	err = h.HandleInbound(context.Background(), Message{Event: SendCommandEvent, Args: []string{"test"}})
	if err != nil {
		t.Errorf("expected nil for unauthorized command event, got %v", err)
	}
	h.setJwt(pFull)

	// 11. SendClose and then SendJson (triggers websocket.ErrCloseSent handling)
	_ = h.SendClose(websocket.CloseNormalClosure, "closing test")
	_ = h.SendJson(Message{Event: server.StatusEvent, Args: []string{"stopped"}})

	// 12. validJwt branches
	// a. Denylisted
	_, pDeny := signToken(t, serverUuid, PermissionConnect)
	pDeny.IssuedAt = jwt.NumericDate(time.Now().Add(-time.Minute))
	tokens.DenyForServer(serverUuid, pDeny.UserUUID)
	h.setJwt(pDeny)
	_, err = h.validJwt()
	if !errors.Is(err, ErrJwtOnDenylist) {
		t.Errorf("expected ErrJwtOnDenylist, got %v", err)
	}

	// b. Missing PermissionConnect
	tokNoConnect := &tokens.WebsocketPayload{
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(time.Now().Add(time.Second * 2)),
			ExpirationTime: jwt.NumericDate(time.Now().Add(time.Hour)),
		},
		UserUUID:    "clean-user-uuid-1",
		Scoped:      tokens.Scoped{Scope: string(tokens.Websocket)},
		ServerUUID:  serverUuid,
		Permissions: []string{PermissionSendCommand},
	}
	h.setJwt(tokNoConnect)
	_, err = h.validJwt()
	if !errors.Is(err, ErrJwtNoConnectPerm) {
		t.Errorf("expected ErrJwtNoConnectPerm, got %v", err)
	}

	// c. Server UUID mismatch
	tokWrongSrv := &tokens.WebsocketPayload{
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(time.Now().Add(time.Second * 2)),
			ExpirationTime: jwt.NumericDate(time.Now().Add(time.Hour)),
		},
		UserUUID:    "clean-user-uuid-2",
		Scoped:      tokens.Scoped{Scope: string(tokens.Websocket)},
		ServerUUID:  otherServerUuid,
		Permissions: []string{PermissionConnect},
	}
	h.setJwt(tokWrongSrv)
	_, err = h.validJwt()
	if !errors.Is(err, ErrJwtUuidMismatch) {
		t.Errorf("expected ErrJwtUuidMismatch, got %v", err)
	}
}

