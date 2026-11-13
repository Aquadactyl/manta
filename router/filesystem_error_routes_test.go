package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/internal/ufs"
	"github.com/pterodactyl/wings/router/downloader"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
)

func TestFilesystemRoutesRejectInvalidPathsAndTypes(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()

	request := func(method, target, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		h.router.ServeHTTP(w, req)
		return w
	}

	// Listing and writing outside the server root fail path validation.
	w := request(http.MethodGet, "/api/servers/"+s.ID()+"/files/list-directory?directory=../../outside", "")
	assert.NotEqual(t, http.StatusOK, w.Code)

	w = request(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=../../outside", "data")
	assert.NotEqual(t, http.StatusNoContent, w.Code)

	w = request(http.MethodPost, "/api/servers/"+s.ID()+"/files/copy", `{"location":"../../outside"}`)
	assert.NotEqual(t, http.StatusNoContent, w.Code)

	// A readable file with no recognized archive format is rejected during the
	// archive space preflight.
	require.NoError(t, s.Filesystem().Writefile("plain.txt", strings.NewReader("plain text")))
	s.Filesystem().SetDiskLimit(1 << 20)
	w = request(http.MethodPost, "/api/servers/"+s.ID()+"/files/decompress", `{"root":"/","file":"plain.txt"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Unsupported URL schemes parse successfully but are not allowed as a pull
	// source.
	w = request(http.MethodPost, "/api/servers/"+s.ID()+"/files/pull", `{"root":"/","url":"ftp://example.com/file"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	require.NoError(t, s.Filesystem().Writefile("parent-file", strings.NewReader("not a directory")))
	w = request(http.MethodPost, "/api/servers/"+s.ID()+"/files/create-directory", `{"name":"child","path":"parent-file"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = request(http.MethodPost, "/api/servers/"+s.ID()+"/files/delete", `{"root":"/parent-file","files":["child"]}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	w = request(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", `{"root":"/parent-file","files":[{"file":"child","mode":"0644"}]}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	w = request(http.MethodPost, "/api/servers/"+s.ID()+"/files/create-directory", `{"name":"child","path":"../../outside"}`)
	assert.NotEqual(t, http.StatusNoContent, w.Code)

	archive := buildTransferTestArchive(t, map[string]string{"large-file.txt": strings.Repeat("x", 16)})
	require.NoError(t, s.Filesystem().Writefile("limited.tar.gz", strings.NewReader(string(archive))))
	s.Filesystem().SetDiskLimit(1)
	w = request(http.MethodPost, "/api/servers/"+s.ID()+"/files/decompress", `{"root":"/","file":"limited.tar.gz"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestFilesystemRoutesRespectEggFileDenylist(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	settings, err := json.Marshal(map[string]any{
		"uuid": transferTestUUID,
		"egg":  map[string]any{"file_denylist": []string{"blocked.txt"}},
	})
	require.NoError(t, err)
	serverConfig := h.panel.config
	serverConfig.Settings = settings
	s, err := h.mgr.InitServer(transferTestUUID, serverConfig)
	require.NoError(t, err)
	require.True(t, h.mgr.AddIfMissing(s))

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/files/copy", body: `{"location":"blocked.txt"}`},
		{method: http.MethodPost, path: "/files/write?file=blocked.txt", body: "blocked"},
		{method: http.MethodPut, path: "/files/rename", body: `{"root":"/","files":[{"from":"blocked.txt","to":"renamed.txt"}]}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/api/servers/"+s.ID()+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer test-token")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.router.ServeHTTP(w, req)
			assert.NotEqual(t, http.StatusNoContent, w.Code)
		})
	}
	assert.Error(t, handleFileUpload("blocked.txt", s, strings.NewReader("blocked"), 100))
}

func TestRenameFilesHandlesMissingSourcesAndExistingTargets(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	require.NoError(t, s.Filesystem().Writefile("source.txt", strings.NewReader("source")))
	require.NoError(t, s.Filesystem().Writefile("target.txt", strings.NewReader("target")))

	rename := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/servers/"+s.ID()+"/files/rename", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.router.ServeHTTP(w, req)
		return w
	}

	missing := rename(`{"root":"/","files":[{"from":"missing.txt","to":"new.txt"}]}`)
	assert.Equal(t, http.StatusNoContent, missing.Code)

	conflict := rename(`{"root":"/","files":[{"from":"source.txt","to":"target.txt"}]}`)
	assert.Equal(t, http.StatusBadRequest, conflict.Code)
}

func TestDecompressRouteReportsBusyAndUnexpectedFailures(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	oldDecompress := decompressServerFile
	t.Cleanup(func() { decompressServerFile = oldDecompress })

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "busy file", err: errors.New("text file busy"), want: http.StatusBadRequest},
		{name: "other failure", err: errors.New("extract failed"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decompressServerFile = func(*server.Server, context.Context, string, string) error { return tc.err }
			req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/decompress", strings.NewReader(`{"root":"/","file":"unused.tar.gz"}`))
			req.Header.Set("Authorization", "Bearer test-token")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.router.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
		})
	}
}

type routeFileInfo struct{ mode os.FileMode }

func (i routeFileInfo) Name() string       { return "test-file" }
func (i routeFileInfo) Size() int64        { return 10 }
func (i routeFileInfo) Mode() os.FileMode  { return i.mode }
func (i routeFileInfo) ModTime() time.Time { return time.Time{} }
func (i routeFileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i routeFileInfo) Sys() any           { return nil }

type routeReadFile struct {
	ufs.File
	err    error
	closed bool
}

func (f *routeReadFile) Read([]byte) (int, error) { return 0, f.err }
func (f *routeReadFile) Close() error {
	f.closed = true
	return nil
}

func TestGetServerFileContentsSpecialFileAndReadError(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	oldOpen := openServerFile
	t.Cleanup(func() { openServerFile = oldOpen })

	pipe := &routeReadFile{}
	openServerFile = func(*server.Server, string) (ufs.File, filesystem.Stat, error) {
		return pipe, filesystem.Stat{FileInfo: routeFileInfo{mode: os.ModeNamedPipe}}, nil
	}
	request := httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=pipe", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	h.router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.True(t, pipe.closed)

	readFailure := &routeReadFile{err: errors.New("read failed")}
	openServerFile = func(*server.Server, string) (ufs.File, filesystem.Stat, error) {
		return readFailure, filesystem.Stat{
			FileInfo: routeFileInfo{mode: 0o600},
			Mimetype: "text/plain",
		}, nil
	}
	request = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=broken", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response = httptest.NewRecorder()
	h.router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.True(t, readFailure.closed)
}

func TestPullRemoteFileForegroundErrors(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	oldExecute := executeRemoteDownload
	t.Cleanup(func() { executeRemoteDownload = oldExecute })

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid destination", err: downloader.ErrInvalidIPAddress, want: http.StatusBadRequest},
		{name: "download failed", err: fmt.Errorf("%w: response rejected", downloader.ErrDownloadFailed), want: http.StatusBadRequest},
		{name: "unexpected failure", err: errors.New("filesystem failed"), want: http.StatusInternalServerError},
		{name: "stat after download", err: nil, want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executeRemoteDownload = func(dl *downloader.Download) error {
				dl.Cancel()
				return tc.err
			}
			root := "/"
			if tc.err == nil {
				root = "/missing-directory"
			}
			body := fmt.Sprintf(`{"root":%q,"url":"https://example.com/file.txt","file_name":"not-created.txt","foreground":true}`, root)
			req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/pull", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer test-token")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.router.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
		})
	}
}
