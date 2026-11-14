package router

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gbrlsnchs/jwt/v3"
	"github.com/google/uuid"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/router/downloader"
	"github.com/pterodactyl/wings/router/tokens"
	wserver "github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/backup"
	"github.com/pterodactyl/wings/server/transfer"
	"github.com/pterodactyl/wings/system"
	"mime/multipart"
	"net"
	"net/netip"
	"net/url"
)

func TestSystemInformationRoutes(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})

	// 1. GET /api/system (v1)
	req := httptest.NewRequest(http.MethodGet, "/api/system", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "architecture")
	assert.Contains(t, w.Body.String(), "cpu_count")

	// 2. GET /api/system?v=2 (v2)
	req = httptest.NewRequest(http.MethodGet, "/api/system?v=2", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "system_usage")
	assert.Contains(t, w.Body.String(), "capabilities")
}

func TestServerFilesystemAndManagementRoutes(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()

	// 1. POST /api/servers/:server/ws/deny
	req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/ws/deny", strings.NewReader(`{"jtis":["jti-1","jti-2"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 2. POST /api/servers/:server/files/create-directory
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/create-directory", strings.NewReader(`{"name":"testdir","path":"/"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 3. POST /api/servers/:server/files/write
	fileContent := []byte("hello world file content")
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=/hello.txt", bytes.NewReader(fileContent))
	req.Header.Set("Authorization", "Bearer test-token")
	req.ContentLength = int64(len(fileContent))
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 4. POST /api/servers/:server/files/copy
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/copy", strings.NewReader(`{"location":"/hello.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 5. GET /api/servers/:server/files/pull
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/pull", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "downloads")

	// 6. DELETE /api/servers/:server/transfer (when not transferring -> 409)
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/transfer", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)

	// 6b. DELETE /api/servers/:server/transfer when transferring -> 202 Accepted
	s.SetTransferring(true)
	tr := transfer.New(t.Context(), s)
	require.True(t, transfer.Outgoing().Add(tr))
	t.Cleanup(func() { transfer.Outgoing().Remove(tr) })
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/transfer", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)

	// 7. DELETE /api/servers/:server/backup/:backup (invalid uuid)
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/backup/invalid-uuid", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 8. DELETE /api/servers/:server/backup/:backup (non-existent uuid)
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/backup/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMoreRouterRoutes(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()

	// 1. GET /api/system/usage
	req := httptest.NewRequest(http.MethodGet, "/api/system/usage", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "cpu_percent")

	// 2. GET /api/servers
	req = httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), s.ID())

	// 3. GET /api/servers/:server
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID(), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), s.ID())

	// 4. POST /api/deauthorize-user
	req = httptest.NewRequest(http.MethodPost, "/api/deauthorize-user", strings.NewReader(`{"user_id":"user1"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 5. GET /api/servers/:server/logs
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/logs?size=50", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 6. POST /api/servers/:server/power (invalid action)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/power", strings.NewReader(`{"action":"invalid"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// 7. POST /api/servers/:server/commands (not running)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/commands", strings.NewReader(`{"commands":["help"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadGateway, w.Code)

	// 8. POST /api/servers/:server/install
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/install", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)

	// 9. POST /api/servers/:server/reinstall
	sReinstall := h.seedServerWithUUID("00000000-0000-4000-8000-000000000088")
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+sReinstall.ID()+"/reinstall", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)

	// Write a file for subsequent file tests
	fileContent := []byte("hello router file content")
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=/test.txt", bytes.NewReader(fileContent))
	req.Header.Set("Authorization", "Bearer test-token")
	req.ContentLength = int64(len(fileContent))
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 10. GET /api/servers/:server/files/contents
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=/test.txt", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, string(fileContent), w.Body.String())

	// 11. GET /api/servers/:server/files/contents with download
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=/test.txt&download=true", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")

	// 12. GET /api/servers/:server/files/list-directory
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/list-directory?directory=/", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "test.txt")

	// 13. PUT /api/servers/:server/files/rename
	req = httptest.NewRequest(http.MethodPut, "/api/servers/"+s.ID()+"/files/rename", strings.NewReader(`{"root":"/","files":[{"from":"test.txt","to":"renamed.txt"}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 14. POST /api/servers/:server/files/chmod
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", strings.NewReader(`{"root":"/","files":[{"file":"renamed.txt","mode":"0644"}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 15. POST /api/servers/:server/files/compress
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/compress", strings.NewReader(`{"root":"/","files":["renamed.txt"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 16. POST /api/servers/:server/files/delete
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/delete", strings.NewReader(`{"root":"/","files":["renamed.txt"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 17. DELETE /api/servers/:server/files/pull/:download
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/files/pull/some-id", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 18. POST /api/servers/:server/sync
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/sync", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 19. POST /api/servers/:server/power (stop action)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/power", strings.NewReader(`{"action":"stop","wait_seconds":10}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)

	// 20. Empty files error responses
	req = httptest.NewRequest(http.MethodPut, "/api/servers/"+s.ID()+"/files/rename", strings.NewReader(`{"root":"/","files":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/compress", strings.NewReader(`{"root":"/","files":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/delete", strings.NewReader(`{"root":"/","files":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", strings.NewReader(`{"root":"/","files":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// 21. Download routes without token
	req = httptest.NewRequest(http.MethodGet, "/download/file", nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.True(t, w.Code >= 400)

	req = httptest.NewRequest(http.MethodGet, "/download/backup", nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.True(t, w.Code >= 400)

	// 22. GET /api/servers (getAllServers)
	req = httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 23. POST /api/servers/:server/install
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/install", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)

	// 24. POST /api/servers/:server/reinstall
	sReinstall2 := h.seedServerWithUUID("00000000-0000-4000-8000-000000000087")
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+sReinstall2.ID()+"/reinstall", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusAccepted, http.StatusConflict}, w.Code)

	// 25. POST /api/servers/:server/ws/deny
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/ws/deny", strings.NewReader(`{"jtis":["jti-1"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 26. POST /api/servers conflict check
	req = httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"uuid":"`+s.ID()+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)

	// 27. POST /api/servers validation error
	req = httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"uuid":"not-a-valid-uuid"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.True(t, w.Code >= 400)

	// 28. POST /api/update with IgnorePanelConfigUpdates = true
	cfg := config.Get()
	cfg.IgnorePanelConfigUpdates = true
	config.Set(cfg)

	req = httptest.NewRequest(http.MethodPost, "/api/update", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"applied":false`)

	// 29. Valid file operations: create file, copy, rename, chmod, delete
	require.NoError(t, os.WriteFile(filepath.Join(s.Filesystem().Path(), "source.txt"), []byte("hello world"), 0644))

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=written.txt", strings.NewReader("sample data written"))
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=written.txt", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "sample data written", w.Body.String())

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/create-directory", strings.NewReader(`{"root":"/","name":"test-dir"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/copy", strings.NewReader(`{"location":"source.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	req = httptest.NewRequest(http.MethodPut, "/api/servers/"+s.ID()+"/files/rename", strings.NewReader(`{"root":"/","files":[{"from":"source.txt","to":"renamed.txt"}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", strings.NewReader(`{"root":"/","files":[{"file":"renamed.txt","mode":"0644"}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/compress", strings.NewReader(`{"root":"/","files":["renamed.txt"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), ".tar.gz")

	// Parse archive filename and decompress
	var archiveResp struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &archiveResp)
	if archiveResp.Name != "" {
		req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/decompress", strings.NewReader(`{"root":"/","file":"`+archiveResp.Name+`"}`))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		h.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/delete", strings.NewReader(`{"root":"/","files":["renamed.txt"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 30. Download file with valid token
	now := time.Now().Add(2 * time.Second)
	tokFile := tokens.FilePayload{
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(time.Hour)),
		},
		Scoped:     tokens.Scoped{Scope: string(tokens.FileDownload)},
		ServerUuid: s.ID(),
		UserUuid:   "user-123",
		FilePath:   "written.txt",
		UniqueId:   uuid.New().String(),
	}
	fileJwtBytes, err := jwt.Sign(&tokFile, config.GetJwtAlgorithm())
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodGet, "/download/file?token="+string(fileJwtBytes), nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "sample data written", w.Body.String())

	// 31. Download backup with valid token
	backupID := uuid.New().String()
	backupDir := config.Get().System.BackupDirectory
	if backupDir == "" {
		backupDir = filepath.Join(t.TempDir(), "backups")
		config.Get().System.BackupDirectory = backupDir
	}
	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, backupID+".tar.gz"), []byte("backup-archive-content"), 0644))

	tokBackup := tokens.BackupPayload{
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(time.Hour)),
		},
		Scoped:     tokens.Scoped{Scope: string(tokens.BackupDownload)},
		ServerUuid: s.ID(),
		UserUuid:   "user-123",
		BackupUuid: backupID,
		UniqueId:   uuid.New().String(),
	}
	backupJwtBytes, err := jwt.Sign(&tokBackup, config.GetJwtAlgorithm())
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodGet, "/download/backup?token="+string(backupJwtBytes), nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "backup-archive-content", w.Body.String())

	// 32. POST /api/servers/:server/commands
	s.Environment.SetState(environment.ProcessRunningState)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/commands", strings.NewReader(`{"commands":["echo 1", "echo 2"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
	s.Environment.SetState(environment.ProcessOfflineState)

	// 33. DELETE /api/servers/:server (deleteServer)
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID(), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestBackupValidationHelpers(t *testing.T) {
	assert.Error(t, validateBackupDownloadUrl("invalid url : //"))
	assert.Error(t, validateBackupDownloadUrl("ftp://example.com/backup.tar.gz"))
	assert.Error(t, validateBackupDownloadUrl("http://127.0.0.1/backup.tar.gz"))

	assert.True(t, isBlockedBackupRestoreIP("invalid-ip", net.IP{1, 2}))
	assert.True(t, isBlockedBackupRestoreIP("127.0.0.1", net.ParseIP("127.0.0.1")))
	assert.False(t, isAllowedBackupRestoreDestination("example.com", netip.MustParseAddr("1.2.3.4")))

	assert.True(t, isSupportedBackupRestoreContentType("application/x-gzip"))
	assert.True(t, isSupportedBackupRestoreContentType("application/gzip; charset=utf-8"))
	assert.False(t, isSupportedBackupRestoreContentType("text/plain"))

	c := backupRestoreHttpClient()
	assert.NotNil(t, c.Transport)
}

func TestMoreFilesAndBackupRoutes(t *testing.T) {
	func() {
		defer func() { _ = recover() }()
		_ = database.Initialize()
	}()
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	next := *config.Get()
	next.System.User.Uid = os.Getuid()
	next.System.User.Gid = os.Getgid()
	next.Api.RemoteDownloadAllowlist = []string{"127.0.0.1"}
	config.Set(&next)

	require.NoError(t, os.MkdirAll(s.Filesystem().Path(), 0755))

	// 1. Pull Remote File
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("pulled content"))
	}))
	t.Cleanup(origin.Close)

	// Bad URL
	req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/pull", strings.NewReader("{\"url\":\"http://\x7f\",\"root\":\"/\"}"))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Foreground pull
	pullBody, _ := json.Marshal(map[string]interface{}{
		"url":        origin.URL + "/test.txt",
		"root":       "/",
		"file_name":  "pulled.txt",
		"foreground": true,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/pull", bytes.NewReader(pullBody))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Background pull
	pullBodyBg, _ := json.Marshal(map[string]interface{}{
		"url":        origin.URL + "/test2.txt",
		"root":       "/",
		"file_name":  "pulled2.txt",
		"foreground": false,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/pull", bytes.NewReader(pullBodyBg))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	var bgResp struct {
		Identifier string `json:"identifier"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &bgResp)
	if bgResp.Identifier != "" {
		req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/files/pull/"+bgResp.Identifier, nil)
		req.Header.Set("Authorization", "Bearer test-token")
		w = httptest.NewRecorder()
		h.router.ServeHTTP(w, req)
	}

	// 2. Rename Files
	req = httptest.NewRequest(http.MethodPut, "/api/servers/"+s.ID()+"/files/rename", strings.NewReader(`{"root":"/","files":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	req = httptest.NewRequest(http.MethodPut, "/api/servers/"+s.ID()+"/files/rename", strings.NewReader(`{"root":"/","files":[{"from":"pulled.txt","to":"pulled_renamed.txt"},{"from":"nonexistent.txt","to":"nowhere.txt"}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 3. Copy File
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/copy", strings.NewReader(`{"location":"pulled_renamed.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 4. Chmod File
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", strings.NewReader(`{"root":"/","files":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", strings.NewReader(`{"root":"/","files":[{"file":"pulled_renamed.txt","mode":"0644"},{"file":"nonexistent.txt","mode":"0644"}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", strings.NewReader(`{"root":"/","files":[{"file":"pulled_renamed.txt","mode":"invalid_mode"}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 5. Upload File (POST /upload/file)
	now := time.Now().Add(2 * time.Second)
	uploadToken := tokens.UploadPayload{
		Payload: jwt.Payload{
			JWTID:          uuid.New().String(),
			IssuedAt:       jwt.NumericDate(now),
			ExpirationTime: jwt.NumericDate(now.Add(time.Hour)),
		},
		Scoped:     tokens.Scoped{Scope: string(tokens.FileUpload)},
		ServerUuid: s.ID(),
		UserUuid:   "user-123",
		UniqueId:   uuid.New().String(),
	}
	uploadJwt, err := jwt.Sign(&uploadToken, config.GetJwtAlgorithm())
	require.NoError(t, err)

	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	part, err := mw.CreateFormFile("files", "test_upload.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte("file upload test data"))
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req = httptest.NewRequest(http.MethodPost, "/upload/file?token="+string(uploadJwt), &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Upload without multipart header -> 400
	uploadToken2 := tokens.UploadPayload{
		Payload: jwt.Payload{
			JWTID:          uuid.New().String(),
			IssuedAt:       jwt.NumericDate(now),
			ExpirationTime: jwt.NumericDate(now.Add(time.Hour)),
		},
		Scoped:     tokens.Scoped{Scope: string(tokens.FileUpload)},
		ServerUuid: s.ID(),
		UserUuid:   "user-123",
		UniqueId:   uuid.New().String(),
	}
	uploadJwt2, err := jwt.Sign(&uploadToken2, config.GetJwtAlgorithm())
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodPost, "/upload/file?token="+string(uploadJwt2), strings.NewReader("bad"))
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 6. Backup Endpoints
	backupUUID := uuid.New().String()
	// Invalid uuid
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup", strings.NewReader(`{"adapter":"wings","uuid":"not-a-uuid"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Invalid adapter
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup", strings.NewReader(`{"adapter":"invalid","uuid":"`+backupUUID+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	// Valid adapter
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup", strings.NewReader(`{"adapter":"wings","uuid":"`+backupUUID+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)

	// Delete Backup
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/backup/not-a-uuid", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/backup/"+uuid.New().String(), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Restore Backup
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup/not-a-uuid/restore", strings.NewReader(`{"adapter":"wings"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup/"+backupUUID+"/restore", strings.NewReader(`{"adapter":"s3"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Non-existent local backup -> 500
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup/"+uuid.New().String()+"/restore", strings.NewReader(`{"adapter":"wings"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	// Existing backup UUID
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup/"+backupUUID+"/restore", strings.NewReader(`{"adapter":"wings"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusAccepted, http.StatusInternalServerError}, w.Code)

	// Reset restoring state after async restore started
	time.Sleep(10 * time.Millisecond)
	s.SetRestoring(false)

	// 7. Transfer Endpoints
	// Keep the upload in flight until cancellation. A refused connection can
	// finish before the duplicate request, making its expected 409 depend on
	// goroutine scheduling.
	received := make(chan struct{}, 1)
	release := make(chan struct{})
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		select {
		case received <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		if tr := transfer.Outgoing().Get(s.ID()); tr != nil {
			tr.Cancel()
		}
		close(release)
		destination.Close()
		require.Eventually(t, func() bool { return transfer.Outgoing().Get(s.ID()) == nil }, 5*time.Second, time.Millisecond)
	})
	transferBody := `{"url":"` + destination.URL + `/transfer","token":"tok"}`

	// Cancel when not transferring
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/transfer", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)

	// Start transfer
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", strings.NewReader(transferBody))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("transfer did not reach the controlled destination")
	}

	// Start transfer again while transferring -> 409
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", strings.NewReader(transferBody))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)

	// Cancel transfer -> 202
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/transfer", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	require.Eventually(t, func() bool {
		return transfer.Outgoing().Get(s.ID()) == nil && !s.IsTransferring()
	}, 5*time.Second, time.Millisecond)
}

func TestMoreServerAndDownloadRoutes(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()

	// 1. getServerLogs
	for _, sz := range []string{"-10", "50", "200"} {
		req := httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/logs?size="+sz, nil)
		req.Header.Set("Authorization", "Bearer test-token")
		w := httptest.NewRecorder()
		h.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "line1")
	}

	// 2. postServerCommands
	// When offline -> 502 Bad Gateway
	req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/commands", strings.NewReader(`{"commands":["say hi"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadGateway, w.Code)

	// When running -> 204 No Content
	if env, ok := s.Environment.(transferTestEnvironment); ok {
		env.SetState(environment.ProcessRunningState)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/commands", strings.NewReader(`{"commands":["say hi","list"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 3. postServerPower
	// Invalid action -> 422
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/power", strings.NewReader(`{"action":"invalid_action"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// Suspended server start -> 400
	s.Config().SetSuspended(true)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/power", strings.NewReader(`{"action":"start"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	s.Config().SetSuspended(false)

	// Valid power actions -> 202
	for _, act := range []string{"stop", "kill"} {
		for i := 0; i < 50 && s.ExecutingPowerAction(); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/power", strings.NewReader(`{"action":"`+act+`"}`))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		h.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusAccepted, w.Code)
	}

	// 4. Filesystem CreateDirectory & Write & Compress & Decompress
	// CreateDirectory valid
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/create-directory", strings.NewReader(`{"name":"nested","path":"/"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// WriteFile with Content-Length: -1 -> 400
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=/testfile.txt", strings.NewReader("hello world"))
	req.Header.Set("Authorization", "Bearer test-token")
	req.ContentLength = -1
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// WriteFile valid -> 204
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=/testfile.txt", strings.NewReader("hello world"))
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// CompressFiles empty files -> 422
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/compress", strings.NewReader(`{"root":"/","files":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// CompressFiles valid -> 200
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/compress", strings.NewReader(`{"root":"/","files":["testfile.txt"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// DecompressFiles unknown format -> 400 or 500
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/decompress", strings.NewReader(`{"root":"/","file":"testfile.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// 5. System configuration update
	config.Update(func(c *config.Configuration) {
		c.IgnorePanelConfigUpdates = true
	})
	req = httptest.NewRequest(http.MethodPost, "/api/update", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"applied":false`)
	config.Update(func(c *config.Configuration) {
		c.IgnorePanelConfigUpdates = false
	})

	// 6. Download file route
	now := time.Now().Add(2 * time.Second)
	fileTokenPayload := &tokens.FilePayload{
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			ExpirationTime: jwt.NumericDate(now.Add(time.Hour)),
			JWTID:          "dl-file-jti-" + uuid.NewString(),
		},
		Scoped:     tokens.Scoped{Scope: string(tokens.FileDownload)},
		ServerUuid: s.ID(),
		UserUuid:   "user-1",
		FilePath:   "/testfile.txt",
		UniqueId:   "unique-" + uuid.NewString(),
	}
	signedFileToken, err := jwt.Sign(fileTokenPayload, config.GetJwtAlgorithm())
	require.NoError(t, err)

	// GET /download/file
	req = httptest.NewRequest(http.MethodGet, "/download/file?token="+string(signedFileToken), nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "hello world", w.Body.String())
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))

	// Replay same token (not unique) -> 404
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// 7. Download backup route
	req = httptest.NewRequest(http.MethodGet, "/download/backup?token=invalid-jwt", nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)
}

func TestDeleteTransferActive(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	tr := h.startInFlightReceive(s)
	defer transfer.Incoming().Remove(tr)

	req := httptest.NewRequest(http.MethodDelete, "/api/transfers/"+s.ID(), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, transfer.StatusCancelling, tr.Status())
}

func TestPostCreateServer(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()

	// 1. Conflict when server already exists
	req := httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"uuid":"`+s.ID()+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)

	// 2. UnprocessableEntity when validation fails
	req = httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"uuid":"invalid-uuid"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// 2b. A non-validation error from the Panel is an internal server error.
	h.panel.configErr = errors.New("panel unavailable")
	req = httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"uuid":"00000000-0000-4000-8000-000000000097"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	h.panel.configErr = nil

	// 3. Accepted when valid
	newUUID := "00000000-0000-4000-8000-000000000099"
	h.panel.config.Settings, _ = json.Marshal(map[string]string{"uuid": newUUID})
	validBody := map[string]any{
		"uuid":                newUUID,
		"start_on_completion": false,
	}
	bodyBytes, _ := json.Marshal(validBody)
	req = httptest.NewRequest(http.MethodPost, "/api/servers", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	time.Sleep(30 * time.Millisecond)

	// 4. Accepted with start_on_completion: true and skip_egg_scripts: true
	newUUID2 := "00000000-0000-4000-8000-000000000098"
	h.panel.config.Settings, _ = json.Marshal(map[string]any{
		"uuid":             newUUID2,
		"skip_egg_scripts": true,
	})
	validBody2 := map[string]any{
		"uuid":                newUUID2,
		"start_on_completion": true,
	}
	bodyBytes2, _ := json.Marshal(validBody2)
	req = httptest.NewRequest(http.MethodPost, "/api/servers", bytes.NewReader(bodyBytes2))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	time.Sleep(50 * time.Millisecond)
}

func TestPostCreateServerConcurrentDuplicate(t *testing.T) {
	panel := &transferTestPanel{configHit: make(chan struct{}, 1), configGate: make(chan struct{})}
	h := newTransferHarness(t, panel)
	serverUUID := "00000000-0000-4000-8000-000000000096"
	settings, err := json.Marshal(map[string]string{"uuid": serverUUID})
	require.NoError(t, err)
	panel.config.Settings = settings
	req := httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"uuid":"`+serverUUID+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.router.ServeHTTP(w, req)
		response <- w
	}()

	select {
	case <-panel.configHit:
	case <-time.After(time.Second):
		t.Fatal("create request did not begin loading server configuration")
	}
	h.seedServerWithUUID(serverUUID)
	close(panel.configGate)
	select {
	case w := <-response:
		assert.Equal(t, http.StatusConflict, w.Code)
	case <-time.After(time.Second):
		t.Fatal("create request did not finish after configuration loaded")
	}
}

func TestPostCreateServerEnvironmentSetupFailure(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	serverUUID := "00000000-0000-4000-8000-000000000095"
	settings, err := json.Marshal(map[string]string{"uuid": serverUUID})
	require.NoError(t, err)
	h.panel.config.Settings = settings

	// Machine ID creation is part of environment setup. Making its parent a file
	// forces that setup step to fail before Docker is contacted.
	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0644))
	oldConfig := *config.Get()
	t.Cleanup(func() { config.Set(&oldConfig) })
	cfg := config.Get()
	cfg.System.MachineID.Enable = true
	cfg.System.MachineID.Directory = filePath
	config.Set(cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"uuid":"`+serverUUID+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	time.Sleep(20 * time.Millisecond)
}

func TestPostCreateServerBackgroundInstallFailure(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	const serverUUID = "00000000-0000-4000-8000-000000000094"
	settings, err := json.Marshal(map[string]any{"uuid": serverUUID, "skip_egg_scripts": false})
	require.NoError(t, err)
	h.panel.config.Settings = settings

	oldCreate, oldInstall := createInstallEnvironment, runServerInstall
	done := make(chan struct{})
	createInstallEnvironment = func(*wserver.Server) error { return nil }
	runServerInstall = func(*wserver.Server) error {
		close(done)
		return errors.New("install failed")
	}
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		createInstallEnvironment, runServerInstall = oldCreate, oldInstall
	})

	req := httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"uuid":"`+serverUUID+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background install did not run")
	}
}

func TestPostCreateServerAutomaticStartDeadline(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	const serverUUID = "00000000-0000-4000-8000-000000000093"
	settings, err := json.Marshal(map[string]any{"uuid": serverUUID, "skip_egg_scripts": true})
	require.NoError(t, err)
	h.panel.config.Settings = settings

	oldCreate, oldInstall, oldStart := createInstallEnvironment, runServerInstall, startServerAfterInstall
	done := make(chan struct{})
	createInstallEnvironment = func(*wserver.Server) error { return nil }
	runServerInstall = func(*wserver.Server) error { return nil }
	startServerAfterInstall = func(*wserver.Server) error {
		close(done)
		return context.DeadlineExceeded
	}
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		createInstallEnvironment, runServerInstall, startServerAfterInstall = oldCreate, oldInstall, oldStart
	})

	req := httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"uuid":"`+serverUUID+`","start_on_completion":true}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automatic start was not attempted")
	}
}

func postCreateServerForInstallCoverage(t *testing.T, h *transferHarness, id string, start bool) *httptest.ResponseRecorder {
	t.Helper()
	settings, err := json.Marshal(map[string]any{"uuid": id, "skip_egg_scripts": true})
	require.NoError(t, err)
	h.panel.config.Settings = settings
	body, err := json.Marshal(map[string]any{"uuid": id, "start_on_completion": start})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/servers", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

func TestPostCreateServerInstallWorkerStartsWithCanceledServer(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	oldCreate, oldInstall, oldWorker := createInstallEnvironment, runServerInstall, startServerInstall
	t.Cleanup(func() {
		createInstallEnvironment, runServerInstall, startServerInstall = oldCreate, oldInstall, oldWorker
	})
	called := false
	createInstallEnvironment = func(*wserver.Server) error { called = true; return nil }
	runServerInstall = func(*wserver.Server) error { called = true; return nil }
	startServerInstall = func(s *wserver.Server, run func()) {
		s.CleanupForDestroy()
		run()
	}
	w := postCreateServerForInstallCoverage(t, h, "00000000-0000-4000-8000-000000000092", false)
	require.Equal(t, http.StatusAccepted, w.Code)
	require.False(t, called)
}

func TestPostCreateServerInstallCancellationSkipsAutomaticStart(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	oldCreate, oldInstall, oldWorker, oldStart := createInstallEnvironment, runServerInstall, startServerInstall, startServerAfterInstall
	t.Cleanup(func() {
		createInstallEnvironment, runServerInstall, startServerInstall, startServerAfterInstall = oldCreate, oldInstall, oldWorker, oldStart
	})
	createInstallEnvironment = func(*wserver.Server) error { return nil }
	runServerInstall = func(s *wserver.Server) error {
		s.CleanupForDestroy()
		return nil
	}
	startServerInstall = func(_ *wserver.Server, run func()) { run() }
	startCalled := false
	startServerAfterInstall = func(*wserver.Server) error { startCalled = true; return nil }
	w := postCreateServerForInstallCoverage(t, h, "00000000-0000-4000-8000-000000000091", true)
	require.Equal(t, http.StatusAccepted, w.Code)
	require.False(t, startCalled)
}

func TestPostServerCopyFileAndFileContents(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()

	require.NoError(t, s.Filesystem().Writefile("hello.txt", strings.NewReader("file content data")))

	// 1. getServerFileContents valid
	req := httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=/hello.txt", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "file content data", w.Body.String())

	// 2. getServerFileContents with download=true
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=/hello.txt&download=true", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")

	// 3. getServerFileContents not found
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=/missing.txt", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusNotFound, http.StatusInternalServerError}, w.Code)

	// 4. postServerCopyFile valid
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/copy", strings.NewReader(`{"location":"/hello.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 5. postServerCopyFile missing file -> 404/500
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/copy", strings.NewReader(`{"location":"/missing.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusNotFound, http.StatusInternalServerError}, w.Code)
}

func TestMoreBackupAndRestoreEndpoints(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()

	// 1. postServerBackup with invalid ignore (>16 wildcards)
	bUUID := "11111111-1111-4111-8111-111111111111"
	req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup", strings.NewReader(`{"adapter":"wings","uuid":"`+bUUID+`","ignore":"`+strings.Repeat("*a", 17)+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 2. postServerBackup with invalid adapter
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup", strings.NewReader(`{"adapter":"unknown","uuid":"`+bUUID+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// 3. postServerBackup with s3 adapter
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup", strings.NewReader(`{"adapter":"s3","uuid":"`+bUUID+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)

	// 4. postServerRestoreBackup S3 missing download_url
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup/"+bUUID+"/restore", strings.NewReader(`{"adapter":"s3"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "download_url")

	// 5. deleteServerBackup when file exists locally
	delUUID := "22222222-2222-4222-8222-222222222222"
	backupFile := filepath.Join(config.Get().System.BackupDirectory, delUUID+".tar.gz")
	require.NoError(t, os.WriteFile(backupFile, []byte("tar-gz-content"), 0644))
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/backup/"+delUUID, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.NoFileExists(t, backupFile)
	time.Sleep(100 * time.Millisecond)
	_ = os.Remove(filepath.Join(config.Get().System.BackupDirectory, bUUID+".tar.gz"))
}

func TestFilesEndpointsAdvanced(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()

	// 1. postServerWriteFile with ContentLength == -1
	req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=test.txt", strings.NewReader("content"))
	req.Header.Set("Authorization", "Bearer test-token")
	req.ContentLength = -1
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Missing Content-Length")

	// 2. postServerWriteFile success
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=hello.txt", strings.NewReader("hello world"))
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 3. postServerCreateDirectory success
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/create-directory", strings.NewReader(`{"name":"somedir","path":"/"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 4. postServerWriteFile conflicting with existing directory
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=somedir", strings.NewReader("conflict"))
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "conflicts with an existing directory")

	// 5. postServerCreateDirectory inside a file (ENOTDIR)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/create-directory", strings.NewReader(`{"name":"child","path":"/hello.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// 6. postServerCompressFiles with no files (422)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/compress", strings.NewReader(`{"root":"/","files":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// 7. postServerCompressFiles success
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/compress", strings.NewReader(`{"root":"/","files":["hello.txt"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "application/tar+gzip")

	// 8. postServerDecompressFiles unknown archive format
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/decompress", strings.NewReader(`{"root":"/","file":"hello.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// 9. postServerPullRemoteFile with deprecated directory field and bad url
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/pull", strings.NewReader(`{"directory":"/","url":"::invalid"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "error occurred while parsing that URL")
}

func TestDownloadAndUploadAdvanced(t *testing.T) {
	func() {
		defer func() { _ = recover() }()
		_ = database.Initialize()
	}()

	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()

	now := time.Now().Add(time.Second)
	bUUID := uuid.New().String()

	// 1. getDownloadBackup with invalid backup UUID in token
	bpBad := tokens.BackupPayload{
		ServerUuid: s.ID(),
		UserUuid:   "usr-1",
		BackupUuid: "not-a-uuid",
		UniqueId:   "unique-bp-1",
		Scoped:     tokens.Scoped{Scope: string(tokens.BackupDownload)},
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(10 * time.Minute)),
		},
	}
	tokenBad, err := jwt.Sign(&bpBad, config.GetJwtAlgorithm())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/download/backup?token="+string(tokenBad), nil)
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusNotFound}, w.Code)

	// 2. getDownloadBackup backup not found (404)
	bpNotFound := tokens.BackupPayload{
		ServerUuid: s.ID(),
		UserUuid:   "usr-1",
		BackupUuid: bUUID,
		UniqueId:   "unique-bp-2",
		Scoped:     tokens.Scoped{Scope: string(tokens.BackupDownload)},
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(10 * time.Minute)),
		},
	}
	tokenNotFound, err := jwt.Sign(&bpNotFound, config.GetJwtAlgorithm())
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodGet, "/download/backup?token="+string(tokenNotFound), nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// 3. getDownloadBackup success (200)
	backupFile := filepath.Join(config.Get().System.BackupDirectory, bUUID+".tar.gz")
	require.NoError(t, os.WriteFile(backupFile, []byte("tar-gz-content"), 0644))
	defer func() { _ = os.Remove(backupFile) }()

	bpValid := tokens.BackupPayload{
		ServerUuid: s.ID(),
		UserUuid:   "usr-1",
		BackupUuid: bUUID,
		UniqueId:   "unique-bp-3",
		Scoped:     tokens.Scoped{Scope: string(tokens.BackupDownload)},
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(10 * time.Minute)),
		},
	}
	tokenValid, err := jwt.Sign(&bpValid, config.GetJwtAlgorithm())
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodGet, "/download/backup?token="+string(tokenValid), nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "tar-gz-content", w.Body.String())

	bpNoServer := bpValid
	bpNoServer.ServerUuid, bpNoServer.UniqueId = uuid.New().String(), "unique-bp-no-server"
	tokenNoServer, err := jwt.Sign(&bpNoServer, config.GetJwtAlgorithm())
	require.NoError(t, err)
	req = httptest.NewRequest(http.MethodGet, "/download/backup?token="+string(tokenNoServer), nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// A backup path that resolves to a directory is a non-not-found lookup error.
	bUUIDDirectory := uuid.New().String()
	require.NoError(t, os.Mkdir(filepath.Join(config.Get().System.BackupDirectory, bUUIDDirectory+".tar.gz"), 0o755))
	bpDirectory := bpValid
	bpDirectory.BackupUuid, bpDirectory.UniqueId = bUUIDDirectory, "unique-bp-directory"
	tokenDirectory, err := jwt.Sign(&bpDirectory, config.GetJwtAlgorithm())
	require.NoError(t, err)
	req = httptest.NewRequest(http.MethodGet, "/download/backup?token="+string(tokenDirectory), nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	// The file can disappear after LocateLocal has completed but before it opens.
	previousOpen := openLocalBackup
	openLocalBackup = func(string) (*os.File, error) { return nil, errors.New("backup open failed") }
	bpOpenFailure := bpValid
	bpOpenFailure.UniqueId = "unique-bp-open-failure"
	tokenOpenFailure, err := jwt.Sign(&bpOpenFailure, config.GetJwtAlgorithm())
	require.NoError(t, err)
	req = httptest.NewRequest(http.MethodGet, "/download/backup?token="+string(tokenOpenFailure), nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	openLocalBackup = previousOpen
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	// 4. getDownloadFile downloading directory (404)
	fpDir := tokens.FilePayload{
		ServerUuid: s.ID(),
		UserUuid:   "usr-1",
		FilePath:   "/",
		UniqueId:   "unique-fp-1",
		Scoped:     tokens.Scoped{Scope: string(tokens.FileDownload)},
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(10 * time.Minute)),
		},
	}
	tokenDir, err := jwt.Sign(&fpDir, config.GetJwtAlgorithm())
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodGet, "/download/file?token="+string(tokenDir), nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// 5. getDownloadFile downloading file success (200)
	require.NoError(t, s.Filesystem().Write("dl.txt", strings.NewReader("download content"), int64(len("download content")), 0644))
	fpFile := tokens.FilePayload{
		ServerUuid: s.ID(),
		UserUuid:   "usr-1",
		FilePath:   "/dl.txt",
		UniqueId:   "unique-fp-2",
		Scoped:     tokens.Scoped{Scope: string(tokens.FileDownload)},
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(10 * time.Minute)),
		},
	}
	tokenFile, err := jwt.Sign(&fpFile, config.GetJwtAlgorithm())
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodGet, "/download/file?token="+string(tokenFile), nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "download content", w.Body.String())

	// 5b. getDownloadFile when the token references a missing file.
	fpMissing := fpFile
	fpMissing.FilePath, fpMissing.UniqueId = "/missing.txt", "unique-fp-missing"
	tokenMissing, err := jwt.Sign(&fpMissing, config.GetJwtAlgorithm())
	require.NoError(t, err)
	req = httptest.NewRequest(http.MethodGet, "/download/file?token="+string(tokenMissing), nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	// 6. postServerUploadFiles not multipart (400)
	upBad := tokens.UploadPayload{
		ServerUuid: s.ID(),
		UserUuid:   "usr-1",
		UniqueId:   "unique-up-1",
		Scoped:     tokens.Scoped{Scope: string(tokens.FileUpload)},
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(10 * time.Minute)),
		},
	}
	tokenUpBad, err := jwt.Sign(&upBad, config.GetJwtAlgorithm())
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodPost, "/upload/file?token="+string(tokenUpBad), strings.NewReader("not multipart"))
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 7. postServerUploadFiles valid multipart (200)
	upValid := tokens.UploadPayload{
		ServerUuid: s.ID(),
		UserUuid:   "usr-1",
		UniqueId:   "unique-up-2",
		Scoped:     tokens.Scoped{Scope: string(tokens.FileUpload)},
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(10 * time.Minute)),
		},
	}
	tokenUpValid, err := jwt.Sign(&upValid, config.GetJwtAlgorithm())
	require.NoError(t, err)

	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	part, err := mw.CreateFormFile("files", "uploaded.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte("uploaded content"))
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req = httptest.NewRequest(http.MethodPost, "/upload/file?token="+string(tokenUpValid), &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRouterServerMoreOperations(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()

	// 1. postServerPower invalid action (422)
	req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/power", strings.NewReader(`{"action":"invalid"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// 2. postServerPower start when suspended (400)
	s.Config().SetSuspended(true)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/power", strings.NewReader(`{"action":"start"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Cannot start or restart a server that is suspended")
	s.Config().SetSuspended(false)

	// 3. postServerCommands when server is stopped (502)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/commands", strings.NewReader(`{"commands":["say hi"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadGateway, w.Code)

	// 4. postServerDenyWSTokens (204)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/ws/deny", strings.NewReader(`{"jtis":["jti-1","jti-2"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 5. postServerSync (204)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/sync", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 6. postServerInstall (202)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/install", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)

	// 7. postServerReinstall (202)
	sReinstall3 := h.seedServerWithUUID("00000000-0000-4000-8000-000000000086")
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+sReinstall3.ID()+"/reinstall", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)

	// 8. postServerTransfer when server is transferring (409)
	s.SetTransferring(true)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", strings.NewReader(`{"url":"http://target:8080","token":"transfer-token"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "cannot be transferred")

	// 9. deleteServer when transferring (204)
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID(), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestServerInstallSyncFailure(t *testing.T) {
	panel := &transferTestPanel{
		configErr: errors.New("panel unavailable"),
		configHit: make(chan struct{}, 2),
	}
	h := newTransferHarness(t, panel)
	s := h.seedServer()

	req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/sync", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	<-panel.configHit

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/install", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	select {
	case <-panel.configHit:
	case <-time.After(time.Second):
		t.Fatal("install did not attempt to sync with the Panel")
	}
}

func TestServerLifecycleHandlersWithCanceledContext(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	s.CtxCancel()

	for _, action := range []string{"install", "reinstall"} {
		req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/"+action, nil)
		req.Header.Set("Authorization", "Bearer test-token")
		w := httptest.NewRecorder()
		h.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusAccepted, w.Code)
	}
	// Let each handler's goroutine observe the canceled context before coverage
	// instrumentation is collected at test shutdown.
	time.Sleep(10 * time.Millisecond)
}

func TestServerPowerBackgroundErrorBranches(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()

	for _, powerErr := range []error{context.Canceled, context.DeadlineExceeded, errors.New("power failed")} {
		hit := make(chan struct{}, 1)
		s.Environment = transferTestEnvironment{waitErr: powerErr, waitHit: hit}
		req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/power", strings.NewReader(`{"action":"stop"}`))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusAccepted, w.Code)
		select {
		case <-hit:
		case <-time.After(time.Second):
			t.Fatal("power handler did not wait for the process to stop")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestServerReinstallConflictsWithPowerAction(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	waitHit := make(chan struct{}, 1)
	waitGate := make(chan struct{})
	s.Environment = transferTestEnvironment{waitHit: waitHit, waitGate: waitGate}
	defer close(waitGate)

	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"action":"stop"}`))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.router.ServeHTTP(w, req)
		return w
	}
	assert.Equal(t, http.StatusAccepted, request("/api/servers/"+s.ID()+"/power").Code)
	select {
	case <-waitHit:
	case <-time.After(time.Second):
		t.Fatal("power action did not acquire the lock")
	}
	assert.Equal(t, http.StatusConflict, request("/api/servers/"+s.ID()+"/reinstall").Code)
}

func TestMoreBackupAndSystemOperations(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()

	// Ensure backup directory exists
	require.NoError(t, os.MkdirAll(config.Get().System.BackupDirectory, 0755))

	// 1. deleteServerBackup invalid UUID (400)
	req := httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/backup/not-a-uuid", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 2. deleteServerBackup not found (404)
	missingUUID := uuid.New().String()
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/backup/"+missingUUID, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// A directory at the backup path is a malformed archive, not a missing backup.
	directoryUUID := uuid.New().String()
	require.NoError(t, os.Mkdir(filepath.Join(config.Get().System.BackupDirectory, directoryUUID+".tar.gz"), 0755))
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/backup/"+directoryUUID, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	// 3. deleteServerBackup exists (204)
	existingUUID := uuid.New().String()
	backupPath := filepath.Join(config.Get().System.BackupDirectory, existingUUID+".tar.gz")
	require.NoError(t, os.WriteFile(backupPath, []byte("tar-content"), 0644))
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/backup/"+existingUUID, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 4. postServerRestoreBackup with local adapter and existing tar.gz
	localUUID := uuid.New().String()
	localBackupFile := filepath.Join(config.Get().System.BackupDirectory, localUUID+".tar.gz")
	require.NoError(t, os.WriteFile(localBackupFile, []byte("fake-tar"), 0644))
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup/"+localUUID+"/restore", strings.NewReader(`{"adapter":"wings","truncate_directory":false}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	time.Sleep(10 * time.Millisecond)
	s.SetRestoring(false)

	// 5. postServerRestoreBackup with S3 adapter and unsupported content type
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad-content-type" {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not gzip"))
			return
		}
		if r.URL.Path == "/bad-status" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
	}))
	defer origin.Close()

	cfg := *config.Get()
	cfg.System.Backups.RestoreHostAllowlist = []string{"127.0.0.1", "localhost"}
	config.Set(&cfg)

	// Unsupported content type
	s3UUID := uuid.New().String()
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup/"+s3UUID+"/restore", strings.NewReader(`{"adapter":"s3","download_url":"`+origin.URL+`/bad-content-type"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not a supported content type")
	s.SetRestoring(false)

	// Bad response status
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/backup/"+s3UUID+"/restore", strings.NewReader(`{"adapter":"s3","download_url":"`+origin.URL+`/bad-status"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid response status")
	s.SetRestoring(false)

	// 6. Direct CheckRedirect tests on backupRestoreHttpClient
	client := backupRestoreHttpClient()
	reqRedirect, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1", nil)
	via := make([]*http.Request, 10)
	assert.Error(t, client.CheckRedirect(reqRedirect, via))

	// 7. getServer and getServerLogs
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID(), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/logs?size=50", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/logs?size=-1", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/logs?size=200", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 8. postDeauthorizeUser
	req = httptest.NewRequest(http.MethodPost, "/api/deauthorize-user", strings.NewReader(`{"user":"test-user","servers":["`+s.ID()+`"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	req = httptest.NewRequest(http.MethodPost, "/api/deauthorize-user", strings.NewReader(`{"user":"test-user","servers":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 9. postCreateServer invalid payload
	req = httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"uuid":""}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusUnprocessableEntity, http.StatusBadRequest}, w.Code)

	// postCreateServer duplicate UUID
	req = httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"uuid":"`+s.ID()+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestDeleteServerBackupRemovalErrors(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	backupDir := config.Get().System.BackupDirectory
	require.NoError(t, os.MkdirAll(backupDir, 0755))

	for _, tc := range []struct {
		name       string
		removeErr  error
		wantStatus int
	}{
		{name: "missing after locate is success", removeErr: os.ErrNotExist, wantStatus: http.StatusNoContent},
		{name: "other removal error is reported", removeErr: errors.New("permission denied"), wantStatus: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backupUUID := uuid.New().String()
			require.NoError(t, os.WriteFile(filepath.Join(backupDir, backupUUID+".tar.gz"), []byte("backup"), 0644))
			previousRemove := removeLocalBackup
			removeLocalBackup = func(*backup.LocalBackup) error { return tc.removeErr }
			t.Cleanup(func() { removeLocalBackup = previousRemove })

			req := httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/backup/"+backupUUID, nil)
			req.Header.Set("Authorization", "Bearer test-token")
			w := httptest.NewRecorder()
			h.router.ServeHTTP(w, req)
			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}
}

func TestFilesEndpointsAdvancedCoverage(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	require.NoError(t, os.MkdirAll(s.Filesystem().Path(), 0755))

	// 1. deleteServerPullRemoteFile
	req := httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/files/pull/some-id", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 2. postServerChmodFile with empty files list (422)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", strings.NewReader(`{"root":"/","files":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// 3. postServerChmodFile with invalid octal mode
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", strings.NewReader(`{"root":"/","files":[{"file":"test.txt","mode":"999"}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// 4. postServerChmodFile with missing file (tolerated, 204)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", strings.NewReader(`{"root":"/","files":[{"file":"missing.txt","mode":"0644"}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 5. postServerDecompressFiles with non-archive file
	txtFile := filepath.Join(s.Filesystem().Path(), "not-archive.txt")
	require.NoError(t, os.WriteFile(txtFile, []byte("plain text content"), 0644))
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/decompress", strings.NewReader(`{"root":"/","file":"not-archive.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// 6. postServerDecompressFiles with valid tar.gz file (204)
	tarFile := filepath.Join(s.Filesystem().Path(), "archive.tar.gz")
	f, err := os.Create(tarFile)
	require.NoError(t, err)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	require.NoError(t, f.Close())

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/decompress", strings.NewReader(`{"root":"/","file":"archive.tar.gz"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 7. Binding errors on various file endpoints (400)
	for _, ep := range []string{
		"/api/servers/" + s.ID() + "/files/rename",
		"/api/servers/" + s.ID() + "/files/copy",
		"/api/servers/" + s.ID() + "/files/delete",
		"/api/servers/" + s.ID() + "/files/pull",
		"/api/servers/" + s.ID() + "/files/create-directory",
		"/api/servers/" + s.ID() + "/files/compress",
		"/api/servers/" + s.ID() + "/files/decompress",
		"/api/servers/" + s.ID() + "/files/chmod",
	} {
		req = httptest.NewRequest(http.MethodPost, ep, strings.NewReader(`{broken-json`))
		if strings.Contains(ep, "rename") {
			req.Method = http.MethodPut
		}
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		h.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	}
	// 8. postServerPullRemoteFile synchronous foreground download
	dlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", "attachment; filename=\"pulled.txt\"")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pulled content"))
	}))
	defer dlServer.Close()

	cfg := *config.Get()
	cfg.Api.RemoteDownloadAllowlist = []string{"127.0.0.1", "localhost"}
	config.Set(&cfg)

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/pull", strings.NewReader(`{
		"url": "`+dlServer.URL+`/file.txt",
		"root": "/",
		"file_name": "pulled.txt",
		"use_header": true,
		"foreground": true
	}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusOK, http.StatusBadRequest}, w.Code)
}

func TestServerTransferAndLifecycleBranches(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	env := transferTestEnvironment{st: system.NewAtomicString(environment.ProcessOfflineState)}
	s.Environment = env

	// 1. postServerPower with negative wait_seconds and kill action
	req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/power", strings.NewReader(`{"action":"kill","wait_seconds":-10}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	time.Sleep(50 * time.Millisecond)

	// 2. postServerCommands with running server
	env.st.Store(environment.ProcessRunningState)

	// Invalid json
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/commands", strings.NewReader(`{broken`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Valid commands
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/commands", strings.NewReader(`{"commands":["say hi","list"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	env.st.Store(environment.ProcessOfflineState)

	// 3. postServerDenyWSTokens invalid json
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/ws/deny", strings.NewReader(`{broken`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 4. postServerReinstall
	sReinstall4 := h.seedServerWithUUID("00000000-0000-4000-8000-000000000085")
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+sReinstall4.ID()+"/reinstall", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusAccepted, http.StatusConflict}, w.Code)

	// 5. deleteServerTransfer when server is transferring but no outgoing transfer object
	s.SetTransferring(true)
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/transfer", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)

	// deleteServerTransfer when outgoing transfer object exists
	tr := transfer.New(context.Background(), s)
	transfer.Outgoing().Add(tr)
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/transfer", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	transfer.Outgoing().Remove(tr)
	s.SetTransferring(false)

	// 6. postServerTransfer invalid json
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", strings.NewReader(`{broken`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// postServerTransfer successful initiation
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", strings.NewReader(`{"url":"http://127.0.0.1:9999","token":"test-token"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	time.Sleep(10 * time.Millisecond)
	s.SetTransferring(false)

	// A destination that consumes and accepts the multipart transfer exercises
	// the successful asynchronous completion path.
	require.NoError(t, s.Filesystem().Writefile("transfer-success.txt", strings.NewReader("transfer payload")))
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", strings.NewReader(`{"url":"`+destination.URL+`/api/transfers","token":"test-token"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	deadline := time.Now().Add(time.Second)
	for transfer.Outgoing().Get(s.ID()) != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	assert.Nil(t, transfer.Outgoing().Get(s.ID()))
	s.SetTransferring(false)

	// A failed push notifies the panel; the notification failure is logged and
	// the local transfer still completes its cleanup.
	h.panel.statusErr = errors.New("panel transfer status unavailable")
	failingDestination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failingDestination.Close()
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", strings.NewReader(`{"url":"`+failingDestination.URL+`/api/transfers","token":"test-token"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	deadline = time.Now().Add(time.Second)
	for transfer.Outgoing().Get(s.ID()) != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	assert.Nil(t, transfer.Outgoing().Get(s.ID()))
	h.panel.statusErr = nil

	// Cancel a transfer after its destination has accepted the request, exercising
	// the context-canceled completion path.
	destinationStarted := make(chan struct{})
	releaseDestination := make(chan struct{})
	cancelDestination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(destinationStarted)
		<-releaseDestination
		w.WriteHeader(http.StatusOK)
	}))
	defer cancelDestination.Close()
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", strings.NewReader(`{"url":"`+cancelDestination.URL+`/api/transfers","token":"test-token"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	select {
	case <-destinationStarted:
	case <-time.After(time.Second):
		t.Fatal("destination did not receive the transfer")
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID()+"/transfer", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	close(releaseDestination)
	deadline = time.Now().Add(time.Second)
	for transfer.Outgoing().Get(s.ID()) != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	assert.Nil(t, transfer.Outgoing().Get(s.ID()))

	// 7. deleteTransfer (/api/transfers/:server)
	// Transfer not found -> 409
	req = httptest.NewRequest(http.MethodDelete, "/api/transfers/nonexistent-server", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)

	// Incoming transfer found -> 202
	inTr := transfer.New(context.Background(), s)
	transfer.Incoming().Add(inTr)
	req = httptest.NewRequest(http.MethodDelete, "/api/transfers/"+s.ID(), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	transfer.Incoming().Remove(inTr)
}

func TestFileOperationsComprehensiveCoverage(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	require.NoError(t, os.MkdirAll(s.Filesystem().Path(), 0755))

	// Write a test file
	testFile := filepath.Join(s.Filesystem().Path(), "sample.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("hello world"), 0644))

	// Create a test directory
	testDir := filepath.Join(s.Filesystem().Path(), "somedir")
	require.NoError(t, os.MkdirAll(testDir, 0755))

	// 1. getServerFileContents with download=true
	req := httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=sample.txt&download=true", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment; filename=\"sample.txt\"")

	// 2. postServerCopyFile
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/copy", strings.NewReader(`{"location":"sample.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 3. putServerRenameFiles empty files list (422)
	req = httptest.NewRequest(http.MethodPut, "/api/servers/"+s.ID()+"/files/rename", strings.NewReader(`{"root":"/","files":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// putServerRenameFiles valid rename
	req = httptest.NewRequest(http.MethodPut, "/api/servers/"+s.ID()+"/files/rename", strings.NewReader(`{"root":"/","files":[{"from":"sample.txt","to":"renamed.txt"}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 4. postServerDeleteFiles empty files list (422)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/delete", strings.NewReader(`{"root":"/","files":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// postServerDeleteFiles valid delete
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/delete", strings.NewReader(`{"root":"/","files":["renamed.txt"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 5. postServerCompressFiles empty files list (422)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/compress", strings.NewReader(`{"root":"/","files":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// 6. postServerWriteFile with ContentLength == -1
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=new.txt", strings.NewReader("content"))
	req.Header.Set("Authorization", "Bearer test-token")
	req.ContentLength = -1
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// postServerWriteFile targeting existing directory
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=somedir", strings.NewReader("content"))
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 7. getServerPullingFiles
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/pull", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 8. postServerCompressFiles no space available (409)
	s.Filesystem().SetDiskLimit(-1)
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/compress", strings.NewReader(`{"root":"/","files":["sample.txt"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
	s.Filesystem().SetDiskLimit(0)

	// 9. postServerDecompressFiles successful extraction (204)
	var tbuf bytes.Buffer
	gz := gzip.NewWriter(&tbuf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "decompressed.txt", Mode: 0644, Size: 4, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("test"))
	_ = tw.Close()
	_ = gz.Close()

	require.NoError(t, os.WriteFile(filepath.Join(s.Filesystem().Path(), "test.tar.gz"), tbuf.Bytes(), 0644))
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/decompress", strings.NewReader(`{"root":"/","file":"test.tar.gz"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.FileExists(t, filepath.Join(s.Filesystem().Path(), "decompressed.txt"))

	// 10. completeIncomingTransfer and deleteTransfer
	tr := h.startInFlightReceive(s)
	completeIncomingTransfer(h.mgr, tr, false)
	tr2 := h.startInFlightReceive(s)
	completeIncomingTransfer(h.mgr, tr2, true)

	// deleteTransfer when not transferring (409)
	req = httptest.NewRequest(http.MethodDelete, "/api/transfers/"+s.ID(), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)

	// deleteTransfer when transferring (202)
	tr3 := h.startInFlightReceive(s)
	transfer.Incoming().Add(tr3)
	req = httptest.NewRequest(http.MethodDelete, "/api/transfers/"+s.ID(), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)

	// 11. Named pipe rejection in getServerFileContents
	fifoPath := filepath.Join(s.Filesystem().Path(), "namedfifo")
	if err := syscall.Mkfifo(fifoPath, 0644); err == nil {
		req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=namedfifo", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		w = httptest.NewRecorder()
		h.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Cannot open files of this type")
		_ = os.Remove(fifoPath)
	}

	// 12. postServerUploadFiles error paths
	h.mgr.AddIfMissing(s)

	// Invalid token
	req = httptest.NewRequest(http.MethodPost, "/upload/file?token=invalid.jwt.token", nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// Valid token format for missing server -> 404
	now := time.Now().Add(2 * time.Second)
	missingTok := tokens.UploadPayload{
		Payload: jwt.Payload{
			Issuer:         "wings-test",
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(10 * time.Minute)),
			Subject:        "user-uuid",
		},
		Scoped: tokens.Scoped{
			Scope: string(tokens.FileUpload),
		},
		ServerUuid: uuid.New().String(),
		UserUuid:   "user-uuid",
		UniqueId:   uuid.New().String(),
	}
	signedTok, err := jwt.Sign(&missingTok, config.GetJwtAlgorithm())
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodPost, "/upload/file?token="+string(signedTok), nil)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Upload token for existing server but no files in multipart -> 400
	validTok := tokens.UploadPayload{
		Payload: jwt.Payload{
			Issuer:         "wings-test",
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(10 * time.Minute)),
			Subject:        "user-uuid",
		},
		Scoped: tokens.Scoped{
			Scope: string(tokens.FileUpload),
		},
		ServerUuid: s.ID(),
		UserUuid:   "user-uuid",
		UniqueId:   uuid.New().String(),
	}
	signedValidTok, err := jwt.Sign(&validTok, config.GetJwtAlgorithm())
	require.NoError(t, err)

	var emptyBuf bytes.Buffer
	mw := multipart.NewWriter(&emptyBuf)
	_ = mw.WriteField("other_field", "value")
	_ = mw.Close()

	req = httptest.NewRequest(http.MethodPost, "/upload/file?token="+string(signedValidTok), &emptyBuf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "No files were found")

	// A malformed subsequent multipart part causes NextPart to fail after parsing starts.
	malformedMultipart := "--broken\r\nContent-Disposition: form-data; name=\"other\"\r\n\r\nvalue\r\n--broken\r\nMalformed Header\r\n\r\n"
	malformedTok := validTok
	malformedTok.UniqueId = uuid.New().String()
	signedMalformedTok, err := jwt.Sign(&malformedTok, config.GetJwtAlgorithm())
	require.NoError(t, err)
	req = httptest.NewRequest(http.MethodPost, "/upload/file?token="+string(signedMalformedTok), strings.NewReader(malformedMultipart))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=broken")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 13. postServerDecompressFiles unknown archive format
	require.NoError(t, os.MkdirAll(s.Filesystem().Path(), 0755))
	notArchiveFile := filepath.Join(s.Filesystem().Path(), "not-archive.txt")
	require.NoError(t, os.WriteFile(notArchiveFile, []byte("plain text content"), 0644))
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/decompress", strings.NewReader(`{"root":"/","file":"not-archive.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// 14. postServerWriteFile with .pteroignore
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=.pteroignore", strings.NewReader("ignored"))
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusNoContent}, w.Code)

	// 15. postServerPullRemoteFile foreground download error
	originFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer originFail.Close()

	nextCfg := *config.Get()
	nextCfg.Api.RemoteDownloadAllowlist = []string{"127.0.0.1"}
	config.Set(&nextCfg)

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/pull", strings.NewReader(`{"root":"/","url":"`+originFail.URL+`/file.txt","foreground":true}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// 16. Named pipe rejected in getServerFileContents
	namedPipePath := filepath.Join(s.Filesystem().Path(), "testpipe")
	_ = syscall.Mknod(namedPipePath, syscall.S_IFIFO|0666, 0)
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=testpipe", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// 17. Download query parameter in getServerFileContents
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=not-archive.txt&download=1", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")

	// 18. Deprecated directory parameter in getServerListDirectory
	req = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/list-directory?directory=/", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 19. postServerCreateDirectory when parent is a regular file
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/create-directory", strings.NewReader(`{"name":"subdir","path":"not-archive.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// 20. postServerPullRemoteFile limit of 3 simultaneous downloads
	u, _ := url.Parse("https://example.com/file.txt")
	dl1 := downloader.New(s, downloader.DownloadRequest{Directory: "/", URL: u, FileName: "f1.txt"})
	dl2 := downloader.New(s, downloader.DownloadRequest{Directory: "/", URL: u, FileName: "f2.txt"})
	dl3 := downloader.New(s, downloader.DownloadRequest{Directory: "/", URL: u, FileName: "f3.txt"})
	defer dl1.Cancel()
	defer dl2.Cancel()
	defer dl3.Cancel()

	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/files/pull", strings.NewReader(`{"root":"/","url":"https://example.com/file4.txt"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "has reached its limit of 3 simultaneous remote file downloads")
}
