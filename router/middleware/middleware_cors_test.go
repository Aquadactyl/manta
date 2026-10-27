package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

func newCORSTestEngine(t *testing.T, configure func(cfg *config.Configuration)) *gin.Engine {
	t.Helper()
	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	cfg.PanelLocation = "http://panel.test"
	configure(cfg)
	config.Set(cfg)

	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(SetAccessControlHeaders())
	engine.GET("/resource", func(c *gin.Context) { c.Status(http.StatusOK) })
	return engine
}

// The private network grant is sent under the response header name browsers
// read.
func TestPrivateNetworkPreflightGrantUsesResponseHeader(t *testing.T) {
	engine := newCORSTestEngine(t, func(cfg *config.Configuration) {
		cfg.AllowCORSPrivateNetwork = true
	})

	req := httptest.NewRequest(http.MethodOptions, "/resource", nil)
	req.Header.Set("Origin", "http://panel.test")
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	require.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Private-Network"))
}

func TestCORSAllowedOriginsBranches(t *testing.T) {
	// Specific allowed origin and wildcard
	engine := newCORSTestEngine(t, func(cfg *config.Configuration) {
		cfg.AllowedOrigins = []string{"http://allowed.test", "http://another.test"}
	})

	// 1. GET with origin matching allowed origin
	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.Header.Set("Origin", "http://allowed.test")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "http://allowed.test", rec.Header().Get("Access-Control-Allow-Origin"))

	// 2. GET with origin not matching allowed origins (falls back to PanelLocation)
	req = httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.Header.Set("Origin", "http://disallowed.test")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "http://panel.test", rec.Header().Get("Access-Control-Allow-Origin"))

	// 3. Wildcard origin
	wildcardEngine := newCORSTestEngine(t, func(cfg *config.Configuration) {
		cfg.AllowedOrigins = []string{"*"}
	})
	req = httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.Header.Set("Origin", "http://random.test")
	rec = httptest.NewRecorder()
	wildcardEngine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
}
