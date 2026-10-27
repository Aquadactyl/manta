package downloader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newUniquePullTestServer(t *testing.T) *server.Server {
	t.Helper()
	previous := config.Get()
	t.Cleanup(func() { config.Set(previous) })
	next := *previous
	next.System.Data = t.TempDir()
	next.System.User.Uid = os.Getuid()
	next.System.User.Gid = os.Getgid()
	next.Api.RemoteDownloadAllowlist = []string{"127.0.0.1"}
	config.Set(&next)

	u := uuid.Must(uuid.NewRandom()).String()
	settings, err := json.Marshal(map[string]string{"uuid": u})
	require.NoError(t, err)
	s, err := server.NewEmptyManager(nil).InitServer(u, remote.ServerConfigurationResponse{
		Settings:             settings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(s.Filesystem().Path(), 0o755))

	t.Cleanup(func() {
		instance.mu.Lock()
		delete(instance.serverCache, u)
		instance.mu.Unlock()
	})
	return s
}

func TestCheckConnectionEdgeCases(t *testing.T) {
	// Missing port
	err := checkConnection(context.Background(), "tcp", "127.0.0.1", nil)
	assert.Error(t, err)

	// Invalid IP
	err = checkConnection(context.Background(), "tcp", "notanip:80", nil)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrInternalResolution))
}

func TestExecuteEdgeCases(t *testing.T) {
	s := newUniquePullTestServer(t)

	// Nil URL
	dl := New(s, DownloadRequest{Directory: "/", URL: nil})
	err := dl.Execute()
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidIPAddress))

	// Invalid URL scheme (e.g. ftp)
	ftpURL, _ := url.Parse("ftp://example.com/file.txt")
	dl = New(s, DownloadRequest{Directory: "/", URL: ftpURL})
	err = dl.Execute()
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidIPAddress))

	// Already started/cancelled
	dl = New(s, DownloadRequest{Directory: "/"})
	dl.started = true
	assert.Equal(t, context.Canceled, dl.Execute())

	dl = New(s, DownloadRequest{Directory: "/"})
	dl.cancelled = true
	assert.Equal(t, context.Canceled, dl.Execute())
}

func TestExecuteDownloadScenarios(t *testing.T) {
	// Start an allowed local HTTP server
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	setAllowlist(t, "127.0.0.1")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/404":
			w.WriteHeader(http.StatusNotFound)
		case "/no-content-length":
			w.Header().Set("Transfer-Encoding", "chunked")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("chunked-data-without-length"))
		case "/valid-disposition":
			w.Header().Set("Content-Disposition", `attachment; filename="custom_file.txt"`)
			w.Header().Set("Content-Length", "11")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("hello world"))
		case "/invalid-disposition":
			w.Header().Set("Content-Disposition", `attachment; filename=`)
			w.Header().Set("Content-Length", "5")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("hello"))
		case "/no-filename-disposition":
			w.Header().Set("Content-Disposition", `attachment`)
			w.Header().Set("Content-Length", "5")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("hello"))
		default:
			w.Header().Set("Content-Length", "12")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("default data"))
		}
	})

	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(l) }()
	defer srv.Close()

	port := l.Addr().(*net.TCPAddr).Port
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	s := newUniquePullTestServer(t)

	t.Run("bad status", func(t *testing.T) {
		u, _ := url.Parse(baseURL + "/404")
		dl := New(s, DownloadRequest{Directory: "/", URL: u, FileName: "test.txt"})
		err := dl.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "bad response status")
	})

	t.Run("missing content length", func(t *testing.T) {
		u, _ := url.Parse(baseURL + "/no-content-length")
		dl := New(s, DownloadRequest{Directory: "/", URL: u, FileName: "test.txt"})
		err := dl.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing ContentLength")
	})

	t.Run("valid content disposition", func(t *testing.T) {
		u, _ := url.Parse(baseURL + "/valid-disposition")
		dl := New(s, DownloadRequest{Directory: "/", URL: u, UseHeader: true})
		err := dl.Execute()
		require.NoError(t, err)
		assert.Equal(t, "/custom_file.txt", dl.Path())
		assert.True(t, dl.Progress() > 0)
	})

	t.Run("invalid content disposition", func(t *testing.T) {
		u, _ := url.Parse(baseURL + "/invalid-disposition")
		dl := New(s, DownloadRequest{Directory: "/", URL: u, UseHeader: true})
		err := dl.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid \"Content-Disposition\" header")
	})

	t.Run("no filename disposition falls back to URL path", func(t *testing.T) {
		u, _ := url.Parse(baseURL + "/no-filename-disposition")
		dl := New(s, DownloadRequest{Directory: "/", URL: u, UseHeader: true})
		err := dl.Execute()
		require.NoError(t, err)
		assert.Equal(t, "/no-filename-disposition", dl.Path())
	})
}

func TestDownloaderCacheAndLifecycle(t *testing.T) {
	s := newUniquePullTestServer(t)

	u, _ := url.Parse("http://example.com/file.tar.gz")
	dl := New(s, DownloadRequest{Directory: "/", URL: u, FileName: "file.tar.gz"})
	assert.NotEmpty(t, dl.Identifier)
	assert.True(t, dl.BelongsTo(s))

	found := ByID(dl.Identifier)
	assert.Equal(t, dl, found)

	bySrv := ByServer(s.ID())
	assert.NotEmpty(t, bySrv)

	// JSON marshaling
	marshaled, err := dl.MarshalJSON()
	assert.NoError(t, err)
	assert.Contains(t, string(marshaled), dl.Identifier)

	// Cancel and remove
	dl.Cancel()
	assert.True(t, dl.cancelled)

	// After completion/cancel, removing it
	instance.remove(dl.Identifier)
	assert.Nil(t, ByID(dl.Identifier))
}
