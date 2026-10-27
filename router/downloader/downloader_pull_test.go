package downloader

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

const pullTestServerUuid = "ffffffff-ffff-4fff-8fff-ffffffffffff"

// newPullTestServer returns a server whose files are stored in a temporary data
// directory and owned by the user running the tests, so that they pass without
// root. Loopback is allowed so that the tests can use a local origin.
func newPullTestServer(t *testing.T) *server.Server {
	t.Helper()
	previous := config.Get()
	t.Cleanup(func() { config.Set(previous) })
	next := *previous
	next.System.Data = t.TempDir()
	next.System.User.Uid = os.Getuid()
	next.System.User.Gid = os.Getgid()
	next.Api.RemoteDownloadAllowlist = []string{"127.0.0.1"}
	config.Set(&next)

	settings, err := json.Marshal(map[string]string{"uuid": pullTestServerUuid})
	require.NoError(t, err)
	s, err := server.NewEmptyManager(nil).InitServer(pullTestServerUuid, remote.ServerConfigurationResponse{
		Settings:             settings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(s.Filesystem().Path(), 0o755))
	return s
}

// A file from a server that compresses responses for clients that accept gzip
// can be pulled.
func TestDownloadAcceptsGzipEncodedResponses(t *testing.T) {
	s := newPullTestServer(t)

	payload := []byte(strings.Repeat("pull test payload\n", 4096))
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, err := zw.Write(payload)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := payload
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			body = compressed.Bytes()
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	t.Cleanup(origin.Close)

	u, err := url.Parse(origin.URL + "/payload.bin")
	require.NoError(t, err)
	require.NoError(t, New(s, DownloadRequest{Directory: "/", URL: u, FileName: "payload.bin"}).Execute())

	got, err := os.ReadFile(filepath.Join(s.Filesystem().Path(), "payload.bin"))
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// Only the name of a file from Content-Disposition is used, so the file is saved
// in the directory that was asked for.
func TestDownloadFilenameFromHeaderIsSavedInRequestedDirectory(t *testing.T) {
	tests := []struct {
		directory string
		want      string
	}{
		{directory: "/chosen/dir", want: "chosen/dir/named.txt"},
		{directory: "chosen/dir", want: "chosen/dir/named.txt"},
		{directory: "", want: "named.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.directory, func(t *testing.T) {
			s := newPullTestServer(t)

			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Disposition", `attachment; filename="../../named.txt"`)
				w.Header().Set("Content-Length", "5")
				_, _ = w.Write([]byte("hello"))
			}))
			t.Cleanup(origin.Close)

			u, err := url.Parse(origin.URL + "/file")
			require.NoError(t, err)
			require.NoError(t, New(s, DownloadRequest{Directory: tt.directory, URL: u, FileName: "file", UseHeader: true}).Execute())

			root := s.Filesystem().Path()
			require.FileExists(t, filepath.Join(root, tt.want))
			require.NoFileExists(t, filepath.Join(filepath.Dir(root), "named.txt"))
		})
	}
}

// A download that fails to connect is reported as a failed download.
func TestDownloadConnectionFailureIsDownloadError(t *testing.T) {
	s := newPullTestServer(t)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	u, err := url.Parse("http://" + addr + "/file")
	require.NoError(t, err)
	err = New(s, DownloadRequest{Directory: "/", URL: u, FileName: "file"}).Execute()
	require.Error(t, err)
	require.True(t, IsDownloadError(err), "got %v", err)
	require.ErrorIs(t, err, ErrDownloadFailed)
}

func TestDownloaderCacheFindAndRemove(t *testing.T) {
	s := newPullTestServer(t)
	u, _ := url.Parse("http://127.0.0.1/test")
	dl1 := New(s, DownloadRequest{Directory: "/", URL: u, FileName: "test1"})
	dl2 := New(s, DownloadRequest{Directory: "/", URL: u, FileName: "test2"})

	// Check ByID
	require.Equal(t, dl1, ByID(dl1.Identifier))
	require.Equal(t, dl2, ByID(dl2.Identifier))
	require.Nil(t, ByID("non-existent-id"))

	// Check ByServer
	serverDownloads := ByServer(s.ID())
	require.Len(t, serverDownloads, 2)

	// Remove dl1, dl2 should remain in serverCache
	instance.remove(dl1.Identifier)
	require.Nil(t, ByID(dl1.Identifier))
	require.Len(t, ByServer(s.ID()), 1)

	// Remove non-existent ID does not panic
	instance.remove("non-existent")

	// Remove dl2, serverCache should now be cleaned up
	instance.remove(dl2.Identifier)
	require.Nil(t, ByID(dl2.Identifier))
	require.Empty(t, ByServer(s.ID()))
}

func TestCheckConnectionInvalidAddressErrors(t *testing.T) {
	ctx := context.Background()
	// Missing port
	err := checkConnection(ctx, "tcp", "invalid-no-port", nil)
	require.Error(t, err)

	// Non-numeric port
	err = checkConnection(ctx, "tcp", "127.0.0.1:abc", nil)
	require.Error(t, err)
}

func TestDownloadExecuteErrors(t *testing.T) {
	s := newPullTestServer(t)

	// 1. Non-200 Status code
	origin404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(origin404.Close)

	u404, _ := url.Parse(origin404.URL + "/404")
	err := New(s, DownloadRequest{Directory: "/", URL: u404, FileName: "f1"}).Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "got bad response status from endpoint")

	// 2. Content-Length < 1 (chunked without Content-Length)
	originChunked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Transfer-Encoding", "chunked")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write([]byte("chunked data"))
	}))
	t.Cleanup(originChunked.Close)

	uChunked, _ := url.Parse(originChunked.URL + "/chunked")
	err = New(s, DownloadRequest{Directory: "/", URL: uChunked, FileName: "f2"}).Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "request is missing ContentLength")

	// 3. Invalid Content-Disposition
	originBadCD := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "5")
		w.Header().Set("Content-Disposition", "attachment; invalid==format")
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(originBadCD.Close)
	uBadCD, _ := url.Parse(originBadCD.URL + "/badcd")
	err = New(s, DownloadRequest{Directory: "/", URL: uBadCD, UseHeader: true}).Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid \"Content-Disposition\" header")

	// 4. Fallback to URL path when FileName is empty
	originPath := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4")
		_, _ = w.Write([]byte("test"))
	}))
	t.Cleanup(originPath.Close)
	uPath, _ := url.Parse(originPath.URL + "/fallback.txt")
	err = New(s, DownloadRequest{Directory: "/", URL: uPath}).Execute()
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(s.Filesystem().Path(), "fallback.txt"))

	// 5. Filesystem create error (target is existing directory)
	require.NoError(t, os.Mkdir(filepath.Join(s.Filesystem().Path(), "existing-dir"), 0o755))
	originOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4")
		_, _ = w.Write([]byte("test"))
	}))
	t.Cleanup(originOK.Close)

	uOK, _ := url.Parse(originOK.URL + "/ok")
	err = New(s, DownloadRequest{Directory: "/", URL: uOK, FileName: "existing-dir"}).Execute()
	require.Error(t, err)
}
