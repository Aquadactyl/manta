package router

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/system"
)

func TestPostUpdateConfigurationRejectsInvalidTokenAndWriteFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pathIsDir  bool
		envTokenID string
		body       string
	}{
		{
			name:       "environment token mismatch",
			envTokenID: "pinned-token-id",
			body:       `{"token_id":"new-token-id","token":"new-token"}`,
		},
		{
			name: "empty token",
			body: `{"token_id":"","token":""}`,
		},
		{
			name:      "configuration destination is a directory",
			pathIsDir: true,
			body:      `{"token_id":"new-token-id","token":"new-token"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WINGS_TOKEN_ID", tc.envTokenID)
			t.Setenv("WINGS_TOKEN", "")
			configPath := filepath.Join(t.TempDir(), "config.yml")
			if tc.pathIsDir {
				require.NoError(t, os.Mkdir(configPath, 0o700))
			}
			cfg, err := config.NewAtPath(configPath)
			require.NoError(t, err)
			if tc.envTokenID != "" {
				cfg.AuthenticationTokenId = "old-token-id"
				cfg.AuthenticationToken = "old-token"
			} else if tc.pathIsDir {
				cfg.AuthenticationTokenId = "old-token-id"
				cfg.AuthenticationToken = "old-token"
			}
			config.Set(cfg)

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/update", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			postUpdateConfiguration(c)

			require.True(t, c.IsAborted())
			require.NotEmpty(t, c.Errors)
		})
	}
}

func TestGetSystemInformationReportsCollectionErrors(t *testing.T) {
	oldReadSystemInformation := readSystemInformation
	t.Cleanup(func() { readSystemInformation = oldReadSystemInformation })
	readSystemInformation = func() (*system.Information, error) {
		return nil, errors.New("system information unavailable")
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/system", nil)
	getSystemInformation(c)

	require.True(t, c.IsAborted())
	require.NotEmpty(t, c.Errors)
}
