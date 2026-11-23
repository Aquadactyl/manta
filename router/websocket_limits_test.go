package router

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	ws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/Aquadactyl/manta/config"
	"github.com/Aquadactyl/manta/router/websocket"
)

func TestWebsocketBoundsDecodedCompressedMessages(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		for _, size := range []int{maxDecodedWebsocketMessage, maxDecodedWebsocketMessage + 1, 1 << 20} {
			t.Run(fmtWebsocketLimitCase(authenticated, size), func(t *testing.T) {
				url := startWebsocketTestServer(t)
				dialer := ws.Dialer{EnableCompression: true}
				conn, response, err := dialer.Dial(url, http.Header{"Origin": {"http://panel.test"}})
				require.NoError(t, err)
				defer conn.Close()
				require.Contains(t, response.Header.Get("Sec-WebSocket-Extensions"), "permessage-deflate")
				if authenticated {
					require.True(t, authenticate(t, conn))
				}
				// An incomplete JSON value is ignored if it is within the decoded
				// limit. Repetition keeps the compressed wire payload below 4 KiB.
				require.NoError(t, conn.WriteMessage(ws.TextMessage, []byte("{"+strings.Repeat(" ", size-1))))
				if size <= maxDecodedWebsocketMessage {
					require.True(t, authenticate(t, conn), "a permitted message must not break authentication or token refresh")
					return
				}
				require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
				for {
					_, _, err = conn.ReadMessage()
					if err != nil {
						var closeErr *ws.CloseError
						require.ErrorAs(t, err, &closeErr)
						require.Equal(t, ws.CloseMessageTooBig, closeErr.Code)
						break
					}
				}
			})
		}
	}
}

func fmtWebsocketLimitCase(authenticated bool, size int) string {
	state := "pending"
	if authenticated {
		state = "authenticated"
	}
	return state + "/" + strconv.Itoa(size)
}

type blockedWebsocketLogEnvironment struct {
	runningEnvironment
	started chan struct{}
	stopped chan struct{}
}

func (e blockedWebsocketLogEnvironment) IsRunning(ctx context.Context) (bool, error) {
	e.started <- struct{}{}
	<-ctx.Done()
	e.stopped <- struct{}{}
	return false, ctx.Err()
}

func TestWebsocketCancelsLogRetrievalOnDisconnect(t *testing.T) {
	checkWebsocketLogCancellation(t, false)
}

func TestWebsocketCancelsLogBodyOnDisconnect(t *testing.T) {
	checkWebsocketLogCancellation(t, true)
}

type blockedWebsocketLogBodyEnvironment struct{ blockedWebsocketLogEnvironment }

func (e blockedWebsocketLogBodyEnvironment) IsRunning(context.Context) (bool, error) {
	return true, nil
}

func (e blockedWebsocketLogBodyEnvironment) ReadlogContext(ctx context.Context, _ int) ([]string, error) {
	_, err := e.blockedWebsocketLogEnvironment.IsRunning(ctx)
	return nil, err
}

func (e blockedWebsocketLogBodyEnvironment) Readlog(int) ([]string, error) {
	panic("legacy log reader must not be used")
}

func checkWebsocketLogCancellation(t *testing.T, body bool) {
	t.Helper()
	url, s := startWebsocketTestServerFor(t)
	env := blockedWebsocketLogEnvironment{started: make(chan struct{}, 10), stopped: make(chan struct{}, 10)}
	s.Environment = env
	if body {
		s.Environment = blockedWebsocketLogBodyEnvironment{env}
	}
	conn, err := dialWebsocket(t, url)
	require.NoError(t, err)
	require.True(t, authenticate(t, conn))
	for i := 0; i < 2; i++ {
		require.NoError(t, conn.WriteJSON(websocket.Message{Event: websocket.SendServerLogsEvent}))
		select {
		case <-env.started:
		case <-time.After(time.Second):
			t.Fatal("expected log operation to start")
		}
	}
	require.NoError(t, conn.Close())
	for i := 0; i < 2; i++ {
		select {
		case <-env.stopped:
		case <-time.After(time.Second):
			t.Fatal("disconnect did not cancel outstanding log work")
		}
	}
}

type blockedWebsocketCommandEnvironment struct {
	runningEnvironment
	started chan struct{}
	release chan struct{}
	stopped chan struct{}
}

func (e blockedWebsocketCommandEnvironment) SendCommand(string) error {
	e.started <- struct{}{}
	<-e.release
	e.stopped <- struct{}{}
	return errors.New("command finished after disconnect")
}

func TestWebsocketBoundsSlowCommandHandlers(t *testing.T) {
	url, s := startWebsocketTestServerFor(t)
	env := blockedWebsocketCommandEnvironment{started: make(chan struct{}, 10), release: make(chan struct{}), stopped: make(chan struct{}, 10)}
	s.Environment = env
	started := 0
	defer func() {
		close(env.release)
		for range started {
			select {
			case <-env.stopped:
			case <-time.After(time.Second):
				t.Error("command worker did not return")
			}
		}
	}()
	conn, err := dialWebsocket(t, url)
	require.NoError(t, err)
	defer conn.Close()
	message := authenticationMessage(t)
	payload, err := websocket.NewTokenPayload([]byte(message.Args[0]))
	require.NoError(t, err)
	payload.Permissions = append(payload.Permissions, websocket.PermissionSendCommand)
	message.Args[0], err = jwt.NewWithClaims(jwt.SigningMethodHS256, payload).SignedString(config.GetJWTKey())
	require.NoError(t, err)
	require.NoError(t, conn.WriteJSON(message))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	var reply websocket.Message
	require.NoError(t, conn.ReadJSON(&reply))
	require.Equal(t, websocket.AuthenticationSuccessEvent, reply.Event)
	require.NoError(t, conn.ReadJSON(&reply))
	require.Equal(t, websocket.Event("status"), reply.Event)
	for range maxInboundWebsocketHandlers {
		require.NoError(t, conn.WriteJSON(websocket.Message{Event: websocket.SendCommandEvent, Args: []string{"list"}}))
		select {
		case <-env.started:
			started++
		case <-time.After(time.Second):
			t.Fatal("expected command operation to start")
		}
	}
	require.NoError(t, conn.WriteJSON(websocket.Message{Event: websocket.SendCommandEvent, Args: []string{"list"}}))
	for {
		require.NoError(t, conn.ReadJSON(&reply))
		if reply.Event == websocket.ThrottledEvent {
			break
		}
	}
	require.Empty(t, env.started, "overflow must not start another worker")
}

func TestWebsocketAdmissionLimitsAndReleasesExactlyOnce(t *testing.T) {
	a := &websocketAdmission{maxTotal: 2, maxPerIP: 1}
	first, ok := a.acquire("one")
	require.True(t, ok)
	_, ok = a.acquire("one")
	require.False(t, ok)
	second, ok := a.acquire("two")
	require.True(t, ok)
	_, ok = a.acquire("three")
	require.False(t, ok)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(first)
	}
	wg.Wait()
	third, ok := a.acquire("three")
	require.True(t, ok)
	second()
	third()
	require.Zero(t, a.total)
	require.Empty(t, a.byIP)
}

func TestWebsocketPendingAdmissionReleasesAfterAuthentication(t *testing.T) {
	url := startWebsocketTestServer(t)
	var first *ws.Conn
	for i := 0; i < 64; i++ {
		conn, err := dialWebsocket(t, url)
		require.NoError(t, err)
		if i == 0 {
			first = conn
		}
	}
	_, response, err := ws.DefaultDialer.Dial(url, http.Header{"Origin": {"http://panel.test"}})
	require.Error(t, err)
	require.NotNil(t, response)
	defer response.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, response.StatusCode)
	require.True(t, authenticate(t, first))
	_, err = dialWebsocket(t, url)
	require.NoError(t, err, "authentication must release pending capacity")
}
