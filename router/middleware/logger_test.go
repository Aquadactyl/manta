package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

func loggerTestManager(t *testing.T) (*server.Manager, *server.Server) {
	t.Helper()
	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	config.Set(cfg)
	s, err := server.New(nil)
	require.NoError(t, err)
	t.Cleanup(s.CleanupForDestroy)
	settings, err := json.Marshal(map[string]string{"uuid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"})
	require.NoError(t, err)
	require.NoError(t, s.SyncWithConfiguration(remote.ServerConfigurationResponse{Settings: settings}))
	manager := server.NewEmptyManager(nil)
	manager.Add(s)
	return manager, s
}

func TestRequestLoggerIsLazyAndSurvivesContextReuse(t *testing.T) {
	previousLog := log.Log
	defer func() { log.Log = previousLog }()
	var entries []*log.Entry
	log.Log = &log.Logger{Level: log.InfoLevel, Handler: log.HandlerFunc(func(e *log.Entry) error {
		entries = append(entries, e)
		return nil
	})}
	manager, s := loggerTestManager(t)
	router := gin.New()
	router.Use(AttachRequestID(), AttachServerManager(manager))
	var retained []*log.Entry
	check := func(c *gin.Context) {
		_, exists := c.Get("logger")
		require.False(t, exists, "successful routing should not eagerly allocate a logger")
		entry := ExtractLogger(c)
		require.Same(t, entry, ExtractLogger(c))
		entry.Info("request")
		emitted := entries[len(entries)-1]
		require.Equal(t, c.Writer.Header().Get("X-Request-Id"), emitted.Fields["request_id"])
		if c.Param("server") != "" {
			require.Equal(t, s.ID(), emitted.Fields["server_id"])
		} else {
			require.NotContains(t, emitted.Fields, "server_id")
		}
		retained = append(retained, entry)
		c.Status(http.StatusNoContent)
	}
	router.GET("/servers/:server", ServerExists(), check)
	router.GET("/global", check)
	var ids []string
	for _, path := range []string{"/servers/" + s.ID(), "/global", "/servers/" + s.ID()} {
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNoContent, res.Code)
		ids = append(ids, res.Header().Get("X-Request-Id"))
	}
	for i, entry := range entries {
		require.Equal(t, ids[i], entry.Fields["request_id"], "entry must remain usable by background tasks after context reuse")
	}
	retained[0].Info("background task after context reuse")
	require.Equal(t, ids[0], entries[len(entries)-1].Fields["request_id"])
	require.NotEqual(t, ids[0], ids[2])
}

func TestServerExistsPreservesCustomLoggerFields(t *testing.T) {
	previousLog := log.Log
	defer func() { log.Log = previousLog }()
	var emitted *log.Entry
	log.Log = &log.Logger{Level: log.InfoLevel, Handler: log.HandlerFunc(func(e *log.Entry) error {
		emitted = e
		return nil
	})}
	manager, s := loggerTestManager(t)
	provided := log.WithField("custom", "retained")
	router := gin.New()
	router.Use(AttachRequestID(), AttachServerManager(manager), func(c *gin.Context) {
		c.Set("logger", provided)
		c.Next()
	})
	router.GET("/servers/:server", ServerExists(), func(c *gin.Context) {
		entry := ExtractLogger(c)
		entry.Info("custom logger")
		require.Equal(t, "retained", emitted.Fields["custom"])
		require.Equal(t, s.ID(), emitted.Fields["server_id"])
		c.Status(http.StatusNoContent)
	})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/servers/"+s.ID(), nil))
	require.Equal(t, http.StatusNoContent, res.Code)
	provided.Info("original logger")
	require.Equal(t, "retained", emitted.Fields["custom"])
	require.NotContains(t, emitted.Fields, "server_id")
}
