package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWebsocketSecurityHelpers(t *testing.T) {
	checkWebsocketAuthentication("auth success")
	defer func() {
		if got := recover(); got != "valid websocket authentication failed: jwt error" {
			t.Fatalf("authentication failure panic = %v", got)
		}
	}()
	checkWebsocketAuthentication("jwt error")
}

func TestWebsocketReadAndMessageHelpers(t *testing.T) {
	if websocketReadFailed(nil) {
		t.Fatal("nil read error was marked as failed")
	}
	if !websocketReadFailed(errors.New("closed")) {
		t.Fatal("read error was not marked as failed")
	}
	if id, ok := benchmarkWebsocketMessageID([]byte("prefix BENCHWS-123-007 suffix"), "BENCHWS-123-"); !ok || id != "007" {
		t.Fatalf("message id = %q, %v", id, ok)
	}
	for _, data := range [][]byte{[]byte("unrelated"), []byte("BENCHWS-123-0")} {
		if id, ok := benchmarkWebsocketMessageID(data, "BENCHWS-123-"); ok {
			t.Fatalf("malformed message returned id %q", id)
		}
	}

	messages := [][]byte{[]byte("unrelated"), []byte("BENCHWS-123-007"), []byte("BENCHWS-123-007"), []byte("BENCHWS-123-008")}
	reads := 0
	accepted := 0
	collectBenchmarkWebsocketMessages(func() ([]byte, error) {
		data := messages[reads]
		reads++
		return data, nil
	}, "BENCHWS-123-", 2, func() { accepted++ })
	if accepted != 2 || reads != 4 {
		t.Fatalf("accepted %d distinct messages after %d reads, want 2 after 4", accepted, reads)
	}
	collectBenchmarkWebsocketMessages(func() ([]byte, error) {
		return nil, errors.New("connection closed")
	}, "BENCHWS-123-", 1, func() { t.Fatal("unexpected message callback") })
}

func TestSocketReportsClosedBeforeAuthResult(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	h := &harness{}
	h.PanelOrigin = "https://panel.example"
	conn, event := h.socket(node{Base: server.URL}, serverInfo("test-uuid"), "token")
	if conn != nil {
		_ = conn.Close()
	}
	if event != "closed" {
		t.Fatalf("socket event = %q, want closed", event)
	}
}

func serverInfo(uuid string) server {
	return server{UUID: uuid}
}
