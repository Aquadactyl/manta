package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/Aquadactyl/manta/router/middleware"
	"github.com/Aquadactyl/manta/router/websocket"
	"github.com/Aquadactyl/manta/server"
	"github.com/gin-gonic/gin"
	ws "github.com/gorilla/websocket"
	"golang.org/x/time/rate"
)

var expectedCloseCodes = []int{
	ws.CloseGoingAway,
	ws.CloseAbnormalClosure,
	ws.CloseNormalClosure,
	ws.CloseNoStatusReceived,
	ws.CloseServiceRestart,
}

// The wire limit applies before decompression. Bound the decoded message too,
// before allocating the full payload or attempting JSON parsing.
const maxDecodedWebsocketMessage = 32 << 10
const maxInboundWebsocketHandlers = 4

func readWebsocketMessage(conn *ws.Conn) (int, []byte, error) {
	kind, reader, err := conn.NextReader()
	if err != nil {
		return kind, nil, err
	}
	payload, err := io.ReadAll(io.LimitReader(reader, maxDecodedWebsocketMessage+1))
	if err != nil {
		return kind, nil, err
	}
	if len(payload) > maxDecodedWebsocketMessage {
		return kind, nil, ws.ErrReadLimit
	}
	return kind, payload, nil
}

var (
	closeServerWebsocketConnectionMu sync.RWMutex
	closeServerWebsocketConnection   = (*ws.Conn).Close

	// maxWebsocketConnections is the maximum number of authenticated websocket
	// connections a server can have open at once.
	maxWebsocketConnections = 30
	// websocketAuthenticationTimeout is the amount of time a websocket connection
	// has to authenticate before it is closed.
	websocketAuthenticationTimeout = 30 * time.Second

	errTooManyWebsockets = errors.New("too many open websocket connections")
)

func closeWebsocketConnection(conn *ws.Conn) error {
	closeServerWebsocketConnectionMu.RLock()
	close := closeServerWebsocketConnection
	closeServerWebsocketConnectionMu.RUnlock()
	return close(conn)
}

func setWebsocketCloseForTest(close func(*ws.Conn) error) func() {
	closeServerWebsocketConnectionMu.Lock()
	previous := closeServerWebsocketConnection
	closeServerWebsocketConnection = close
	closeServerWebsocketConnectionMu.Unlock()
	return func() {
		closeServerWebsocketConnectionMu.Lock()
		closeServerWebsocketConnection = previous
		closeServerWebsocketConnectionMu.Unlock()
	}
}

// getServerWebsocket returns a handler that upgrades a connection to a websocket
// and passes events along between, allowing at most maxConnections authenticated
// connections to a server, each of which must authenticate within authTimeout.
func getServerWebsocket(maxConnections int, authTimeout time.Duration) gin.HandlerFunc {
	pending := &websocketAdmission{maxTotal: 512, maxPerIP: 64}
	return func(c *gin.Context) {
		release, ok := pending.acquire(c.ClientIP())
		if !ok {
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "Too many pending websocket connections."})
			return
		}
		defer release()
		handleServerWebsocket(c, maxConnections, authTimeout, release)
	}
}

func handleServerWebsocket(c *gin.Context, maxConnections int, authTimeout time.Duration, releasePending func()) {
	manager := middleware.ExtractManager(c)
	s, _ := manager.Get(c.Param("server"))

	// Limit the total number of authenticated websockets that can be open at any one
	// time for a server instance. This applies across all users connected to the server,
	// and is not applied on a per-user basis.
	if s.Websockets().Len() >= maxConnections {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Too many open websocket connections.",
		})

		return
	}

	c.Header("Content-Security-Policy", "default-src 'self'")
	c.Header("X-Frame-Options", "DENY")

	// Create a context that can be canceled when the user disconnects from this
	// socket that will also cancel listeners running in separate threads. If the
	// connection itself is terminated listeners using this context will also be
	// closed.
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	handler, err := websocket.GetHandler(s, c.Writer, c.Request, c)
	if err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	// Track this connection on the server once it authenticates so that we can close them
	// all programmatically when access to the server is revoked. Connections only count
	// towards the limit once they have authenticated, and those that do not
	// authenticate in time are closed.
	//
	// Messages are handled in their own goroutines which can outlive this function, so
	// a connection that authenticates after it has closed is not tracked.
	// Connections that do not authenticate in time are closed.
	var (
		trackMu sync.Mutex
		closed  bool
	)
	authTimer := time.AfterFunc(authTimeout, cancel)
	defer authTimer.Stop()
	handler.OnAuthenticated(func() error {
		trackMu.Lock()
		defer trackMu.Unlock()
		if closed || !authTimer.Stop() {
			return context.Canceled
		}
		if !s.Websockets().TryPush(handler.UUID(), &cancel, handler.User, maxConnections) {
			cancel()
			return errTooManyWebsockets
		}
		releasePending()
		return nil
	})
	handler.Logger().Debug("opening connection to server websocket")
	defer func() {
		trackMu.Lock()
		closed = true
		trackMu.Unlock()
		s.Websockets().Remove(handler.UUID())
	}()

	go func() {
		// When the main context is canceled (through disconnect, server deletion, or server
		// suspension) close the connection itself.
		<-ctx.Done()
		handler.Logger().Debug("closing connection to server websocket")
		if err := closeWebsocketConnection(handler.Connection); err != nil {
			handler.Logger().WithError(err).Error("failed to close websocket connection")
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			return
		// If the server is deleted we need to send a close message to the connected client
		// so that they disconnect since there will be no more events sent along. Listen for
		// the request context being closed to break this loop, otherwise this routine will
		// be left hanging in the background.
		case <-s.Context().Done():
			cancel()
			break
		}
	}()

	// Due to how websockets are handled we need to connect to the socket
	// and _then_ abort it if the server is suspended. You cannot capture
	// the HTTP response in the websocket client, thus we connect and then
	// immediately close with failure.
	if s.IsSuspended() {
		_ = handler.SendClose(4409, "server is suspended")

		return
	}

	// There is a separate rate limiter that applies to individual message types
	// within the actual websocket logic handler. _This_ rate limiter just exists
	// to avoid enormous floods of data through the socket since we need to parse
	// JSON each time. This rate limit realistically should never be hit since this
	// would require sending 50+ messages a second over the websocket (no more than
	// 10 per 200ms).
	var throttled bool
	rl := rate.NewLimiter(rate.Every(time.Millisecond*200), 10)
	activeHandlers := make(chan struct{}, maxInboundWebsocketHandlers)

	for {
		t, p, err := readWebsocketMessage(handler.Connection)
		if err != nil {
			if errors.Is(err, ws.ErrReadLimit) {
				_ = handler.SendClose(ws.CloseMessageTooBig, "message exceeds allowed size")
			} else if ws.IsUnexpectedCloseError(err, expectedCloseCodes...) {
				handler.Logger().WithField("error", err).Warn("error handling websocket message for server")
			}
			break
		}

		if !rl.Allow() {
			if !throttled {
				throttled = true
				_ = handler.SendThrottled("global")
			}
			continue
		}

		// Ignore other message types. The reader enforces both the existing
		// compressed wire limit and the decoded limit before parsing JSON.
		if t != ws.TextMessage {
			continue
		}

		// Discard and JSON parse errors into the void and don't continue processing this
		// specific socket request. If we did a break here the client would get disconnected
		// from the socket, which is NOT what we want to do.
		var j websocket.Message
		if err := json.Unmarshal(p, &j); err != nil {
			continue
		}

		// Slow operations must not accumulate an unbounded number of goroutines.
		// Keep the existing throttled event so clients can retry after the burst.
		select {
		case activeHandlers <- struct{}{}:
			throttled = false
		default:
			if !throttled {
				throttled = true
				_ = handler.SendThrottled("global")
			}
			continue
		}

		go func(msg websocket.Message) {
			defer func() { <-activeHandlers }()
			// Close this connection if handling the message panics.
			defer func() {
				if r := recover(); r != nil {
					handler.Logger().WithField("panic", r).Error("recovered from panic while handling websocket message")
					cancel()
				}
			}()
			if err := handler.HandleInbound(ctx, msg); err != nil {
				if errors.Is(err, server.ErrSuspended) {
					cancel()
				} else {
					_ = handler.SendErrorJSON(msg, err)
				}
			}
		}(j)
	}
}
