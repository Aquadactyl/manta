package cmd

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/apex/log"
	"github.com/pterodactyl/wings/config"
)

func TestServeConfiguredAPIInvokesDefaultTLSHandler(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	oldFatal := rootFatal
	t.Cleanup(func() { rootFatal = oldFatal })
	rootFatal = func(log.Fields, string) {}
	api := config.ApiConfiguration{}
	api.Ssl.Enabled = true
	api.Ssl.CertificateFile = filepath.Join(t.TempDir(), "missing.crt")
	api.Ssl.KeyFile = filepath.Join(t.TempDir(), "missing.key")
	serveConfiguredAPI(&http.Server{TLSConfig: &tls.Config{}}, listener, api, config.SystemConfiguration{}, false, "")
}

func TestServeConfiguredAPIInitializesAutoTLS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	oldHTTP, oldTLS := serveAutoTLSHTTPCommand, serveTLSCommand
	t.Cleanup(func() { serveAutoTLSHTTPCommand, serveTLSCommand = oldHTTP, oldTLS })
	serveAutoTLSHTTPCommand = func(http.Handler) {}
	var got *http.Server
	serveTLSCommand = func(s *http.Server, _ net.Listener, _, _ string) error {
		got = s
		return http.ErrServerClosed
	}
	s := &http.Server{TLSConfig: &tls.Config{}}
	sys := config.SystemConfiguration{RootDirectory: t.TempDir()}
	serveConfiguredAPI(s, listener, config.ApiConfiguration{}, sys, true, "example.com")
	if got != s {
		t.Fatal("auto-TLS server was not passed to TLS serving")
	}
	if len(s.TLSConfig.NextProtos) == 0 {
		t.Fatal("auto-TLS ALPN protocol was not configured")
	}
}

func TestServeConfiguredAPIReportsListenFailures(t *testing.T) {
	oldHTTP, oldTLS, oldAutoHTTP, oldFatal := serveHTTPCommand, serveTLSCommand, serveAutoTLSHTTPCommand, rootFatal
	t.Cleanup(func() {
		serveHTTPCommand, serveTLSCommand, serveAutoTLSHTTPCommand, rootFatal = oldHTTP, oldTLS, oldAutoHTTP, oldFatal
	})
	var messages []string
	rootFatal = func(_ log.Fields, message string) { messages = append(messages, message) }
	serveHTTPCommand = func(*http.Server, net.Listener) error { return errors.New("http failed") }
	serveTLSCommand = func(*http.Server, net.Listener, string, string) error { return errors.New("tls failed") }
	serveAutoTLSHTTPCommand = func(http.Handler) {}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	sys := config.SystemConfiguration{RootDirectory: t.TempDir()}
	serveConfiguredAPI(&http.Server{TLSConfig: &tls.Config{}}, listener, config.ApiConfiguration{}, sys, true, "example.com")
	api := config.ApiConfiguration{}
	api.Ssl.Enabled = true
	serveConfiguredAPI(&http.Server{TLSConfig: &tls.Config{}}, listener, api, sys, false, "")
	serveConfiguredAPI(&http.Server{}, listener, config.ApiConfiguration{}, sys, false, "")
	if len(messages) != 3 {
		t.Fatalf("reported %d listener failures, want 3", len(messages))
	}
	before := len(messages)
	reportRootHTTPListenError(nil)
	if len(messages) != before {
		t.Fatal("nil listen error was reported")
	}
	reportRootHTTPListenError(errors.New("bind failed"))
	if len(messages) != before+1 || messages[len(messages)-1] != "failed to configure HTTP server" {
		t.Fatal("listen error was not reported")
	}
}

func TestDefaultAutoTLSHTTPServerLogsListenError(t *testing.T) {
	old := serveAutoTLSHTTPListen
	t.Cleanup(func() { serveAutoTLSHTTPListen = old })
	done := make(chan struct{})
	serveAutoTLSHTTPListen = func(s *http.Server) error {
		if s.Addr != ":http" {
			t.Errorf("autocert HTTP server address = %q", s.Addr)
		}
		close(done)
		return errors.New("listen failed")
	}
	serveAutoTLSHTTPCommand(http.NotFoundHandler())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("autocert HTTP listener was not started")
	}
}

func TestDefaultAutoTLSHTTPListenerReturnsInvalidAddressError(t *testing.T) {
	if err := serveAutoTLSHTTPListen(&http.Server{Addr: "invalid address"}); err == nil {
		t.Fatal("expected ListenAndServe to reject an invalid address")
	}
}
