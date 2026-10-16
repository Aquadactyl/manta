package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

func (h *harness) token(n node, s server, scope string, modify func(map[string]any)) string {
	claims := map[string]any{"iat": time.Now().Unix() - 1, "nbf": time.Now().Unix() - 5, "exp": time.Now().Add(time.Hour).Unix(), "jti": "benchmark", "scope": scope, "server_uuid": s.UUID, "user_uuid": h.SFTP.UserUUID, "unique_id": fmt.Sprintf("benchmark-%d", time.Now().UnixNano()), "permissions": []string{"websocket.connect", "control.console"}}
	if modify != nil {
		modify(claims)
	}
	b, err := json.Marshal(claims)
	must(err)
	enc := base64.RawURLEncoding
	message := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(b)
	mac := hmac.New(sha256.New, []byte(n.Token))
	_, err = mac.Write([]byte(message))
	must(err)
	return message + "." + enc.EncodeToString(mac.Sum(nil))
}
func (h *harness) socket(n node, s server, token string) (*websocket.Conn, string) {
	address := strings.Replace(n.Base, "http://", "ws://", 1) + "/api/servers/" + s.UUID + "/ws"
	c, _, err := websocket.DefaultDialer.Dial(address, http.Header{"Origin": []string{h.PanelOrigin}})
	must(err)
	must(c.SetReadDeadline(time.Now().Add(5 * time.Second)))
	must(c.WriteJSON(map[string]any{"event": "auth", "args": []string{token}}))
	for i := 0; i < 10; i++ {
		var v struct {
			Event string `json:"event"`
		}
		if err := c.ReadJSON(&v); err != nil {
			return c, "closed"
		}
		if v.Event == "auth success" || v.Event == "jwt error" || v.Event == "daemon error" {
			return c, v.Event
		}
	}
	return c, "no authentication result"
}
func (h *harness) websocketLoad(variant string, n node, s server, round int) {
	prefix := fmt.Sprintf("BENCHWS-%d-", time.Now().UnixNano())
	commands := []string{}
	for i := 0; i < 100; i++ {
		commands = append(commands, fmt.Sprintf("say %s%03d", prefix, i))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	lat := []float64{}
	ready := make(chan struct{})
	var started time.Time
	for i := 0; i < 16; i++ {
		c, event := h.socket(n, s, h.token(n, s, "websocket", nil))
		if event != "auth success" {
			panic("valid websocket authentication failed: " + event)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer c.Close()
			<-ready
			must(c.SetReadDeadline(time.Now().Add(12 * time.Second)))
			received := map[string]bool{}
			for len(received) < 100 {
				_, b, err := c.ReadMessage()
				if err != nil {
					return
				}
				p := strings.Index(string(b), prefix)
				if p < 0 || len(b) < p+len(prefix)+3 {
					continue
				}
				id := string(b[p+len(prefix) : p+len(prefix)+3])
				if !received[id] {
					received[id] = true
					mu.Lock()
					lat = append(lat, float64(time.Since(started).Microseconds())/1000)
					mu.Unlock()
				}
			}
		}()
	}
	started = time.Now()
	close(ready)
	h.commands(n, s, commands...)
	wg.Wait()
	row := distribution(lat)
	row["type"] = "measurement"
	row["test"] = "websocket_16_subscribers_100_lines"
	row["variant"] = variant
	row["round"] = round
	row["expected_messages"] = 1600
	row["received_messages"] = len(lat)
	h.add(row)
}
func (h *harness) security() {
	h.startAll()
	for _, variant := range []string{"manta", "wings"} {
		n := h.Nodes[variant]
		s := h.find(n, "paper")
		{
			sc, c := h.sftp(n, s)
			must(sc.MkdirAll("bench"))
			must(sc.Close())
			must(c.Close())
		}
		base := "/api/servers/" + s.UUID
		status, _, err := h.request(n, "GET", "/api/system", nil, false, false)
		must(err)
		h.add(map[string]any{"type": "check", "test": "unauthenticated_api", "variant": variant, "status": status, "pass": status == 401})
		for _, test := range []struct {
			name    string
			body    []byte
			chunked bool
			want    int
		}{
			{"oversized_json_known", append([]byte(`{"name":"oversize","path":"/bench"}`), bytes.Repeat([]byte(" "), 4<<20)...), false, 413},
			{"oversized_json_chunked", append([]byte(`{"name":"oversize-chunked","path":"/bench"}`), bytes.Repeat([]byte(" "), 4<<20)...), true, 413},
			{"multiple_json_documents", []byte(`{"name":"multiple-json","path":"/bench"} {}`), false, 400},
		} {
			status, _, err := h.request(n, "POST", base+"/files/create-directory", test.body, test.chunked, true)
			must(err)
			h.add(map[string]any{"type": "check", "test": test.name, "variant": variant, "status": status, "pass": status == test.want})
		}
		for _, test := range []struct {
			name   string
			modify func(map[string]any)
			accept bool
		}{
			{"valid_websocket_token", nil, true},
			{"future_issued_token", func(c map[string]any) { c["iat"] = time.Now().Add(2 * time.Minute).Unix() }, false},
			{"future_not_before_token", func(c map[string]any) { c["nbf"] = time.Now().Add(2 * time.Minute).Unix() }, false},
			{"expired_token", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }, false},
			{"missing_expiry_token", func(c map[string]any) { delete(c, "exp") }, false},
		} {
			c, event := h.socket(n, s, h.token(n, s, "websocket", test.modify))
			must(c.Close())
			accepted := event == "auth success"
			h.add(map[string]any{"type": "check", "test": test.name, "variant": variant, "event": event, "accepted": accepted, "pass": accepted == test.accept})
		}
		payload := bytes.Repeat([]byte("manta-stream-test-"), 524288)
		status, _, err = h.request(n, "POST", base+"/files/write?file=/bench/stream-security.bin", payload, false, true)
		must(err)
		readStatus, b, err := h.request(n, "GET", base+"/files/contents?file=/bench/stream-security.bin", nil, false, true)
		must(err)
		h.add(map[string]any{"type": "check", "test": "raw_stream_above_json_limit", "variant": variant, "bytes": len(payload), "write_status": status, "read_status": readStatus, "pass": status == 204 && readStatus == 200 && bytes.Equal(b, payload)})
		token := h.token(n, s, "file-download", func(c map[string]any) {
			c["file_path"] = "bench/stream-security.bin"
			c["exp"] = time.Now().Add(3 * time.Hour).Unix()
		})
		first, _, err := h.request(n, "GET", "/download/file?token="+url.QueryEscape(token), nil, false, false)
		must(err)
		second, _, err := h.request(n, "GET", "/download/file?token="+url.QueryEscape(token), nil, false, false)
		must(err)
		h.add(map[string]any{"type": "check", "test": "one_time_file_token", "variant": variant, "first_status": first, "replay_status": second, "pass": first == 200 && second == 404})
		marker := fmt.Sprintf("synthetic-secret-benchmark-%s-%d", variant, time.Now().UnixNano())
		status, _, err = h.request(n, "POST", "/upload/file?token="+marker, nil, false, false)
		must(err)
		logs := h.dk("/containers/" + n.Container + "/logs?stdout=true&stderr=true&tail=150")
		leaked := bytes.Contains(logs, []byte(marker))
		h.add(map[string]any{"type": "check", "test": "invalid_upload_token_log_redaction", "variant": variant, "status": status, "synthetic_marker_leaked": leaked, "pass": !leaked})
		h.api(n, "POST", base+"/files/delete", map[string]any{"root": "/bench", "files": []string{"ssrf.txt"}})
		status, b, err = h.request(n, "POST", base+"/files/pull", []byte(`{"root":"/bench","url":"http://127.0.0.1:19090/sentinel.txt","file_name":"ssrf.txt","foreground":true}`), false, true)
		must(err)
		readStatus, contents, err := h.request(n, "GET", base+"/files/contents?file=/bench/ssrf.txt", nil, false, true)
		must(err)
		retrieved := readStatus == 200 && strings.TrimSpace(string(contents)) == "controlled-loopback-benchmark"
		h.add(map[string]any{"type": "check", "test": "ssrf_loopback_blocked", "variant": variant, "status": status, "pass": !retrieved, "response": string(b), "loopback_content_retrieved": retrieved, "file_status": readStatus})
		sc, c := h.sftp(n, s)
		must(sc.MkdirAll("bench"))
		linkErr := sc.Symlink("/etc/passwd", "bench/escape-link")
		opened := false
		if linkErr == nil {
			f, err := sc.Open("bench/escape-link")
			if err == nil {
				opened = true
				_ = f.Close()
			}
			_ = sc.Remove("bench/escape-link")
		}
		must(sc.Close())
		must(c.Close())
		h.add(map[string]any{"type": "check", "test": "sftp_symlink_containment", "variant": variant, "creation_rejected": linkErr != nil, "outside_file_opened": opened, "pass": !opened})
		for _, format := range []struct{ name, suffix string }{{"full_uuid_sftp", s.UUID}, {"modern_identifier_sftp", s.Identifier}} {
			sc, c, err := h.sftpSuffix(n, format.suffix)
			accepted := err == nil
			if accepted {
				must(sc.Close())
				must(c.Close())
			}
			h.add(map[string]any{"type": "check", "test": format.name, "variant": variant, "accepted": accepted, "pass": accepted})
		}
	}
}
