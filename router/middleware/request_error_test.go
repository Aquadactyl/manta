package middleware

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/ufs"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestRequestErrorMethodsAndFilesystemErrors(t *testing.T) {
	baseErr := errors.New("something went wrong")
	re := NewError(baseErr)
	assert.Contains(t, re.Cause().Error(), "something went wrong")
	assert.Equal(t, "something went wrong", re.Error())

	re.SetMessage("custom message")
	re.SetStatus(http.StatusTeapot)
	assert.Equal(t, "custom message", re.msg)
	assert.Equal(t, http.StatusTeapot, re.status)

	// Test nil cause
	reNil := &RequestError{}
	code, msg := reNil.asFilesystemError()
	assert.Equal(t, 0, code)
	assert.Empty(t, msg)

	// Test asFilesystemError variants
	testCases := []struct {
		err          error
		expectedCode int
		expectedMsg  string
	}{
		{
			err:          filesystem.NewBadPathResolution("foo", "bar"),
			expectedCode: http.StatusNotFound,
			expectedMsg:  "The requested resources was not found on the system.",
		},
		{
			err:          errors.New("filesystem: file access prohibited: egg denylist"),
			expectedCode: http.StatusForbidden,
			expectedMsg:  "This file cannot be modified: present in egg denylist.",
		},
		{
			err:          errors.New("filesystem: is a directory"),
			expectedCode: http.StatusBadRequest,
			expectedMsg:  "Cannot perform that action: file is a directory.",
		},
		{
			err:          ufs.ErrNotRegular,
			expectedCode: http.StatusBadRequest,
			expectedMsg:  "Cannot perform that action: not a regular file.",
		},
		{
			err:          errors.New("filesystem: not enough disk space"),
			expectedCode: http.StatusBadRequest,
			expectedMsg:  "There is not enough disk space available to perform that action.",
		},
		{
			err:          errors.New("file name too long"),
			expectedCode: http.StatusBadRequest,
			expectedMsg:  "Cannot perform that action: file name is too long.",
		},
		{
			err:          &os.SyscallError{Syscall: "readdirent", Err: errors.New("directory error")},
			expectedCode: http.StatusNotFound,
			expectedMsg:  "The requested directory does not exist.",
		},
		{
			err:          errors.New("unknown error"),
			expectedCode: 0,
			expectedMsg:  "",
		},
	}

	for _, tc := range testCases {
		r := &RequestError{err: tc.err}
		status, msg := r.asFilesystemError()
		assert.Equal(t, tc.expectedCode, status)
		assert.Equal(t, tc.expectedMsg, msg)
	}
}

func TestCaptureErrorsMiddleware(t *testing.T) {
	r := gin.New()
	r.Use(AttachRequestID())
	r.Use(CaptureErrors())

	// Route 1: normal success
	r.GET("/ok", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Route 2: io.EOF error
	r.GET("/eof", func(c *gin.Context) {
		CaptureAndAbort(c, io.EOF)
	})

	// Route 3: filesystem error
	r.GET("/fs-err", func(c *gin.Context) {
		CaptureAndAbort(c, filesystem.NewBadPathResolution("bad", "dest"))
	})

	// Route 4: deadline exceeded error
	r.GET("/timeout", func(c *gin.Context) {
		CaptureAndAbort(c, context.DeadlineExceeded)
	})

	// 1. OK
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/ok", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 2. EOF
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/eof", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "parsable format")

	// 3. Filesystem Error
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/fs-err", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "not found")

	// 4. Timeout Error
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/timeout", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "could not process this request in time")
}

func TestMiddlewareExtractors(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// ExtractApiClient
	client := remote.New("http://localhost")
	AttachApiClient(client)(c)
	assert.Equal(t, client, ExtractApiClient(c))

	// ExtractServer
	srv := &server.Server{}
	c.Set("server", srv)
	assert.Equal(t, srv, ExtractServer(c))

	// RemoteDownloadEnabled
	config.Set(&config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	})

	// Disabled
	config.Update(func(cfg *config.Configuration) {
		cfg.Api.DisableRemoteDownload = true
	})
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	RemoteDownloadEnabled()(c2)
	assert.True(t, c2.IsAborted())

	// Enabled
	config.Update(func(cfg *config.Configuration) {
		cfg.Api.DisableRemoteDownload = false
	})
	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	RemoteDownloadEnabled()(c3)
	assert.False(t, c3.IsAborted())
}

func TestRequireAuthorization(t *testing.T) {
	config.Set(&config.Configuration{
		AuthenticationToken: "secret-node-token",
		Token: config.Token{
			Token: "secret-node-token",
		},
	})

	r := gin.New()
	r.Use(RequireAuthorization())
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// 1. Missing header
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/protected", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "Bearer", w.Header().Get("WWW-Authenticate"))

	// 2. Wrong scheme
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Basic 12345")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// 3. Wrong token
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// 4. Correct token
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer secret-node-token")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMiddlewarePanicsAndBranches(t *testing.T) {
	// 1. Panics when missing from context
	cEmpty, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Panics(t, func() {
		ExtractLogger(cEmpty)
	})
	assert.Panics(t, func() {
		ExtractServer(cEmpty)
	})
	assert.Panics(t, func() {
		ExtractApiClient(cEmpty)
	})
	assert.Panics(t, func() {
		ExtractManager(cEmpty)
	})

	// 2. ServerExists when server param is empty or not found
	r := gin.New()
	mgr := &server.Manager{}
	r.Use(AttachServerManager(mgr))
	r.Use(ServerExists())
	r.GET("/servers/:server", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/servers/nonexistent-uuid", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// 3. CaptureErrors with generic error (not RequestError)
	rGeneric := gin.New()
	rGeneric.Use(AttachRequestID())
	rGeneric.Use(CaptureErrors())
	rGeneric.GET("/generic-err", func(c *gin.Context) {
		_ = c.Error(errors.New("plain generic error"))
	})
	rGeneric.GET("/pre-status", func(c *gin.Context) {
		c.Status(http.StatusTeapot)
		_ = c.Error(errors.New("status already set"))
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/generic-err", nil)
	rGeneric.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/pre-status", nil)
	rGeneric.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTeapot, w.Code)

	// 4. RequestError.Abort with server in context, context canceled, and status < 500
	cAbort, _ := gin.CreateTestContext(httptest.NewRecorder())
	cAbort.Request, _ = http.NewRequest("GET", "/test-canceled", nil)
	cAbort.Writer.Header().Set("X-Request-Id", "req-123")
	s := &server.Server{}
	cAbort.Set("server", s)

	reCancel := NewError(errors.New("context canceled"))
	reCancel.Abort(cAbort, http.StatusBadRequest)
	assert.Equal(t, http.StatusBadRequest, cAbort.Writer.Status())

	// Abort with status < 500 and empty msg
	cAbort2, _ := gin.CreateTestContext(httptest.NewRecorder())
	cAbort2.Request, _ = http.NewRequest("GET", "/test-not-found", nil)
	cAbort2.Writer.Header().Set("X-Request-Id", "req-456")
	reNotFound := NewError(errors.New("not found"))
	reNotFound.Abort(cAbort2, http.StatusNotFound)
	assert.Equal(t, http.StatusNotFound, cAbort2.Writer.Status())
}
