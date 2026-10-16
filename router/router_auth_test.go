package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apex/log"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

// Grouping authentication must preserve protection and live credential rotation
// on both global and server-specific endpoints.
func TestProtectedRouteAuthenticationAndRotation(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	for _, path := range []string{"/api/servers", "/api/servers/" + s.ID()} {
		request := func(token string) int {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			res := httptest.NewRecorder()
			h.router.ServeHTTP(res, req)
			return res.Code
		}
		require.Equal(t, http.StatusUnauthorized, request(""))
		require.Equal(t, http.StatusForbidden, request("wrong-token"))
		require.Equal(t, http.StatusOK, request("test-token"))
		cfg := config.Get()
		cfg.Token.Token = "rotated-token"
		config.Set(cfg)
		require.Equal(t, http.StatusForbidden, request("test-token"))
		require.Equal(t, http.StatusOK, request("rotated-token"))
		cfg.Token.Token = "test-token"
		config.Set(cfg)
	}
}

func TestAccessLoggingFollowsLevelAndRedactsQuery(t *testing.T) {
	previousLog := log.Log
	defer func() { log.Log = previousLog }()
	var entries []*log.Entry
	logger := &log.Logger{Level: log.InfoLevel, Handler: log.HandlerFunc(func(e *log.Entry) error {
		entries = append(entries, e)
		return nil
	})}
	log.Log = logger
	h := newTransferHarness(t, &transferTestPanel{})
	request := func() {
		h.router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/missing?token=private-marker", nil))
	}
	request()
	require.Empty(t, entries)
	logger.Level = log.DebugLevel
	request()
	require.Len(t, entries, 1)
	require.Equal(t, "GET /missing", entries[0].Message)
	require.NotEmpty(t, entries[0].Fields["request_id"])
	for _, e := range entries {
		require.False(t, strings.Contains(e.Message, "private-marker"))
	}
}
