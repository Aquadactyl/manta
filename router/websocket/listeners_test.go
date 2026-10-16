package websocket

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"

	"github.com/pterodactyl/wings/server"
)

// Exercise the bus, listener, authorization gate and actual WebSocket wire.
func TestBusEventsRetainWebsocketAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name       string
		topic      string
		permission string
		want       bool
		expired    bool
	}{
		{"stats", server.StatsEvent, "", true, false},
		{"install_denied", server.InstallOutputEvent, "", false, false},
		{"install_allowed", server.InstallOutputEvent, PermissionReceiveInstall, true, false},
		{"backup_denied", server.BackupCompletedEvent + ":1234", "", false, false},
		{"backup_allowed", server.BackupCompletedEvent + ":1234", PermissionReceiveBackups, true, false},
		{"transfer_denied", server.TransferLogsEvent, "", false, false},
		{"transfer_allowed", server.TransferLogsEvent, PermissionReceiveTransfer, true, false},
		{"expired_token", server.StatsEvent, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, client := setupHandlerClient(t)
			_, token := signToken(t, serverUuid, PermissionConnect, tc.permission)
			h.setJwt(token)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- h.listenForServerEvents(ctx) }()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(2 * time.Second):
					t.Error("event listener did not stop")
				}
			})

			// Wait for listener registration without relying on a fixed sleep.
			ready, stopReady := context.WithCancel(ctx)
			t.Cleanup(stopReady)
			publisherDone := make(chan struct{})
			go func() {
				defer close(publisherDone)
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-ready.Done():
						return
					case <-ticker.C:
						h.server.Events().Publish("test ready", nil)
					}
				}
			}()
			if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var message Message
			if err := client.ReadJSON(&message); err != nil || message.Event != "test ready" {
				t.Fatalf("listener readiness: message=%#v error=%v", message, err)
			}
			stopReady()
			<-publisherDone

			if tc.expired {
				_, expired := signToken(t, serverUuid, PermissionConnect)
				expired.ExpirationTime = jwt.NumericDate(time.Now().Add(-time.Hour))
				h.setJwt(expired)
			}
			h.server.Events().Publish(tc.topic, map[string]uint64{"counter": 18446744073709551615})
			h.server.Events().Publish("test complete", nil)
			seen := false
			for {
				if err := client.ReadJSON(&message); err != nil {
					t.Fatal(err)
				}
				if message.Event == "test ready" {
					continue
				}
				if tc.expired {
					if message.Event != JwtErrorEvent {
						t.Fatalf("expired token received event %#v", message)
					}
					break
				}
				if message.Event == "test complete" {
					break
				}
				if string(message.Event) != strings.SplitN(tc.topic, ":", 2)[0] || len(message.Args) != 1 || message.Args[0] != `{"counter":18446744073709551615}` {
					t.Fatalf("unexpected event: %#v", message)
				}
				seen = true
			}
			if seen != tc.want {
				t.Fatalf("event delivered=%v; want %v", seen, tc.want)
			}
		})
	}
}
