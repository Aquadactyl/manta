package transfer

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/progress"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
	"github.com/pterodactyl/wings/system"
)

func newManagerTestTransfer(t *testing.T, uuid string) *Transfer {
	t.Helper()
	s, err := server.New(nil)
	require.NoError(t, err)
	s.Config().Uuid = uuid
	return New(context.Background(), s)
}

// Only one transfer can be tracked for a server, and removing one transfer does
// not remove a different transfer for the same server.
func TestManagerTracksOneTransferPerServer(t *testing.T) {
	m := NewManager()
	first := newManagerTestTransfer(t, "server")
	second := newManagerTestTransfer(t, "server")

	require.True(t, m.Add(first))
	require.False(t, m.Add(second))

	m.Remove(second)
	require.Same(t, first, m.Get("server"))

	m.Remove(first)
	require.Nil(t, m.Get("server"))
}

func TestTransferMethodsAndStatus(t *testing.T) {
	// Status.String()
	require.Equal(t, "pending", StatusPending.String())
	require.Equal(t, "processing", StatusProcessing.String())
	require.Equal(t, "cancelling", StatusCancelling.String())
	require.Equal(t, "cancelled", StatusCancelled.String())
	require.Equal(t, "failed", StatusFailed.String())
	require.Equal(t, "completed", StatusCompleted.String())

	tr := newManagerTestTransfer(t, "test-server-transfer")
	require.NotNil(t, tr.Context())
	require.Equal(t, StatusPending, tr.Status())

	// Status change
	tr.SetStatus(StatusProcessing)
	require.Equal(t, StatusProcessing, tr.Status())

	// SendMessage & Error & Log
	tr.SendMessage("test transfer message")
	tr.Error(nil, "some error occurred")
	require.NotNil(t, tr.Log())

	// Server nil Log()
	trNilServer := &Transfer{}
	require.NotNil(t, trNilServer.Log())

	// Archive & Progress
	arch := NewArchive(tr, 1000)
	require.NotNil(t, arch)
	require.NotNil(t, arch.Progress())

	// Cancel
	tr.Cancel()
	require.Equal(t, StatusCancelling, tr.Status())

	// Cancel when already cancelling is a no-op
	tr.Cancel()
	require.Equal(t, StatusCancelling, tr.Status())

	// Cancel when completed is a no-op
	tr.SetStatus(StatusCompleted)
	tr.Cancel()
	require.Equal(t, StatusCompleted, tr.Status())
}

func TestManagerIncomingOutgoing(t *testing.T) {
	require.NotNil(t, Incoming())
	require.NotNil(t, Outgoing())
}

func TestPushArchiveToTarget(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	serverDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "test.txt"), []byte("transfer data"), 0644))

	fsys, err := filesystem.New(serverDir, 0, nil)
	require.NoError(t, err)

	var receivedChecksum string
	var receivedFileContent []byte

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer secret-token", r.Header.Get("Authorization"))
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if part.FormName() == "archive" {
				receivedFileContent, _ = io.ReadAll(part)
			} else if part.FormName() == "checksum" {
				csum, _ := io.ReadAll(part)
				receivedChecksum = string(csum)
			}
			_ = part.Close()
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer ts.Close()

	tr := newManagerTestTransfer(t, "transfer-push")
	arch := &Archive{
		archive: &filesystem.Archive{
			Filesystem: fsys,
			Progress:   progress.NewProgress(100),
		},
	}
	tr.archive = arch

	// Test Stream directly
	buf := new(bytes.Buffer)
	require.NoError(t, arch.Stream(context.Background(), buf))
	assert.True(t, buf.Len() > 0)

	// Test PushArchiveToTarget
	resp, err := tr.PushArchiveToTarget(ts.URL, "Bearer secret-token")
	require.NoError(t, err)
	assert.Equal(t, `{"success":true}`, string(resp))
	assert.NotEmpty(t, receivedChecksum)
	assert.True(t, len(receivedFileContent) > 0)
}

func TestPushArchiveToTarget_ErrorCases(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	serverDir := t.TempDir()
	fsys, err := filesystem.New(serverDir, 0, nil)
	require.NoError(t, err)

	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errServer.Close()

	tr := newManagerTestTransfer(t, "transfer-err")
	tr.archive = &Archive{
		archive: &filesystem.Archive{
			Filesystem: fsys,
			Progress:   progress.NewProgress(100),
		},
	}

	_, err = tr.PushArchiveToTarget(errServer.URL, "token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestTransferArchiveCreation(t *testing.T) {
	cfgDir := t.TempDir()
	cfg, err := config.NewAtPath(filepath.Join(cfgDir, "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token-12345678"
	cfg.System.Data = filepath.Join(cfgDir, "data")
	require.NoError(t, os.MkdirAll(cfg.System.Data, 0755))
	config.Set(cfg)

	uuid := "00000000-0000-0000-0000-000000000001"
	srvDir := filepath.Join(cfg.System.Data, uuid)
	require.NoError(t, os.MkdirAll(srvDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(srvDir, "hello.txt"), []byte("data"), 0644))

	s, err := server.NewEmptyManager(nil).InitServer(uuid, remote.ServerConfigurationResponse{
		Settings:             []byte(`{"uuid":"` + uuid + `"}`),
		ProcessConfiguration: &remote.ProcessConfiguration{},
	})
	require.NoError(t, err)

	tr := New(context.Background(), s)
	arch, err := tr.Archive()
	require.NoError(t, err)
	require.NotNil(t, arch)

	// Cached archive
	arch2, err := tr.Archive()
	require.NoError(t, err)
	require.Same(t, arch, arch2)

	// PushArchiveToTarget error paths
	// 1. Invalid URL
	_, err = tr.PushArchiveToTarget("://invalid-url", "token")
	assert.Error(t, err)

	// 2. Unreachable host
	_, err = tr.PushArchiveToTarget("http://127.0.0.1:1", "token")
	assert.Error(t, err)

	// 3. Cancelled context
	ctxCancel, cancelFunc := context.WithCancel(context.Background())
	cancelFunc()
	trCancel := New(ctxCancel, s)
	trCancel.archive = arch
	_, err = trCancel.PushArchiveToTarget("http://127.0.0.1", "token")
	assert.Error(t, err)

	// 4. Cancel with nil t.cancel
	trNilCancel := &Transfer{status: system.NewAtomic(StatusPending)}
	trNilCancel.Cancel()
	assert.Equal(t, StatusPending, trNilCancel.Status())
}

func TestIsValidRedirectURL(t *testing.T) {
	valid := []string{
		"http://127.0.0.1:1/api/transfers",
		"http://127.0.0.1:9999/transfer",
		"https://example.com/api/transfers",
		"https://node.example.com:8080/api/transfers",
		"http://[::1]:8080/api/transfers",
		"http://example.com/api/transfers?key=value&foo=bar",
	}
	for _, u := range valid {
		assert.True(t, IsValidRedirectURL(u), "expected %s to be valid", u)
	}

	invalid := []string{
		"",
		"://invalid-url",
		"ftp://example.com",
		"http://",
		"http://attacker.com@internal/",
		"http://internal/#attacker.com",
		"javascript:alert(1)",
		"http://[invalid-ipv6",
		"http://\nnewline",
	}
	for _, u := range invalid {
		assert.False(t, IsValidRedirectURL(u), "expected %s to be invalid", u)
	}
}

func TestPushArchiveToTarget_AdvancedBranches(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	serverDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "test.txt"), []byte("transfer data"), 0o644))
	fsys, err := filesystem.New(serverDir, 0, nil)
	require.NoError(t, err)

	// 1. Redirect response hits CheckRedirect and returns unexpected status code 302
	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		http.Redirect(w, r, "http://example.com/other", http.StatusFound)
	}))
	defer redirectSrv.Close()

	tr1 := newManagerTestTransfer(t, "transfer-redirect")
	tr1.archive = &Archive{
		archive: &filesystem.Archive{
			Filesystem: fsys,
			Progress:   progress.NewProgress(100),
		},
	}
	_, err = tr1.PushArchiveToTarget(redirectSrv.URL, "token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "302")

	// 2. Stream error (BaseDirectory does not exist) while server returns 200 OK -> hits errChan error path
	okNoReadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 512)
		_, _ = r.Body.Read(buf)
		w.WriteHeader(http.StatusOK)
	}))
	defer okNoReadSrv.Close()

	tr2 := newManagerTestTransfer(t, "transfer-stream-err")
	tr2.archive = &Archive{
		archive: &filesystem.Archive{
			Filesystem:    fsys,
			BaseDirectory: "/nonexistent_base_dir_for_test",
			Progress:      progress.NewProgress(100),
		},
	}
	_, err = tr2.PushArchiveToTarget(okNoReadSrv.URL, "token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multipart error")

	// 3. Truncated response body -> hits io.ReadAll(res.Body) error path
	truncatedBodySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Length", "5000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}
	}))
	defer truncatedBodySrv.Close()

	tr3 := newManagerTestTransfer(t, "transfer-truncated-body")
	tr3.archive = &Archive{
		archive: &filesystem.Archive{
			Filesystem: fsys,
			Progress:   progress.NewProgress(100),
		},
	}
	_, err = tr3.PushArchiveToTarget(truncatedBodySrv.URL, "token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read response body")

	// 4. Non-200 status code from destination
	badStatusSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badStatusSrv.Close()

	tr4 := newManagerTestTransfer(t, "transfer-bad-status")
	tr4.archive = &Archive{
		archive: &filesystem.Archive{
			Filesystem: fsys,
			Progress:   progress.NewProgress(100),
		},
	}
	_, err = tr4.PushArchiveToTarget(badStatusSrv.URL, "token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code from destination: 500")
}

func TestTransferArchiveCacheAndRedirectURLs(t *testing.T) {
	assert.False(t, IsValidRedirectURL("ftp://example.com"))
	assert.False(t, IsValidRedirectURL("http://"))
	assert.False(t, IsValidRedirectURL("http://user:pass@example.com"))
	assert.True(t, IsValidRedirectURL("http://127.0.0.1:8080/path"))
	assert.False(t, isValidRedirectUrl("invalid://url"))

	tr := newManagerTestTransfer(t, "transfer-cache")
	dataDir := t.TempDir()
	fsys, err := filesystem.New(dataDir, 1024*1024, []string{})
	require.NoError(t, err)
	arch := &Archive{
		archive: &filesystem.Archive{
			Filesystem: fsys,
			Progress:   progress.NewProgress(100),
		},
	}
	tr.archive = arch

	a1, err := tr.Archive()
	require.NoError(t, err)
	assert.Same(t, arch, a1)

	_, err = tr.PushArchiveToTarget("invalid-url", "token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid target URL")
}
