package router

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/server/transfer"
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
	req = httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/reinstall", nil)
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
}
