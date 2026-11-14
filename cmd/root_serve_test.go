package cmd

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/apex/log"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme"

	"github.com/pterodactyl/wings/config"
)

func TestServeConfiguredAPIModes(t *testing.T) {
	oldServeHTTP, oldServeTLS, oldServeAutoTLSHTTP, oldFatal := serveHTTPCommand, serveTLSCommand, serveAutoTLSHTTPCommand, rootFatal
	t.Cleanup(func() {
		serveHTTPCommand, serveTLSCommand, serveAutoTLSHTTPCommand, rootFatal = oldServeHTTP, oldServeTLS, oldServeAutoTLSHTTP, oldFatal
	})

	t.Run("plain HTTP handles closed and failed servers", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			serveErr  error
			wantFatal string
		}{
			{name: "closed", serveErr: http.ErrServerClosed},
			{name: "failed", serveErr: errors.New("listen failed"), wantFatal: "failed to configure HTTP server"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var fatal string
				rootFatal = func(_ log.Fields, message string) { fatal = message }
				serveHTTPCommand = func(*http.Server, net.Listener) error { return tc.serveErr }
				s := &http.Server{TLSConfig: &tls.Config{}}
				serveConfiguredAPI(s, nil, config.ApiConfiguration{}, config.SystemConfiguration{}, false, "")
				require.Nil(t, s.TLSConfig)
				require.Equal(t, tc.wantFatal, fatal)
			})
		}
	})

	t.Run("configured TLS passes certificate paths and reports failures", func(t *testing.T) {
		api := config.ApiConfiguration{}
		api.Ssl.Enabled = true
		api.Ssl.CertificateFile = "server.crt"
		api.Ssl.KeyFile = "server.key"
		var certFile, keyFile, fatal string
		rootFatal = func(_ log.Fields, message string) { fatal = message }
		serveTLSCommand = func(_ *http.Server, _ net.Listener, cert, key string) error {
			certFile, keyFile = cert, key
			return errors.New("TLS failed")
		}
		serveConfiguredAPI(&http.Server{}, nil, api, config.SystemConfiguration{}, false, "")
		require.Equal(t, "server.crt", certFile)
		require.Equal(t, "server.key", keyFile)
		require.Equal(t, "failed to configure HTTPS server", fatal)
	})

	t.Run("automatic TLS configures protocols and handles a closed server", func(t *testing.T) {
		var autoTLSHTTPStarted bool
		var certFile, keyFile string
		serveAutoTLSHTTPCommand = func(handler http.Handler) { autoTLSHTTPStarted = handler != nil }
		serveTLSCommand = func(_ *http.Server, _ net.Listener, cert, key string) error {
			certFile, keyFile = cert, key
			return http.ErrServerClosed
		}
		rootFatal = func(_ log.Fields, message string) { t.Errorf("unexpected fatal: %s", message) }
		s := &http.Server{TLSConfig: &tls.Config{}}
		serveConfiguredAPI(s, nil, config.ApiConfiguration{}, config.SystemConfiguration{RootDirectory: t.TempDir()}, true, "manta.example")
		require.True(t, autoTLSHTTPStarted)
		require.Empty(t, certFile)
		require.Empty(t, keyFile)
		require.NotNil(t, s.TLSConfig.GetCertificate)
		require.Contains(t, s.TLSConfig.NextProtos, acme.ALPNProto)
	})

	t.Run("automatic TLS reports failures", func(t *testing.T) {
		serveAutoTLSHTTPCommand = func(http.Handler) {}
		serveTLSCommand = func(*http.Server, net.Listener, string, string) error { return errors.New("auto TLS failed") }
		var fatal string
		rootFatal = func(_ log.Fields, message string) { fatal = message }
		serveConfiguredAPI(&http.Server{TLSConfig: &tls.Config{}}, nil, config.ApiConfiguration{}, config.SystemConfiguration{}, true, "manta.example")
		require.Equal(t, "failed to configure HTTP server using auto-tls", fatal)
	})
}
