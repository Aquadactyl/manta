package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanceledFileBatchesLeaveFilesUntouched(t *testing.T) {
	for _, tt := range []struct{ name, method, body string }{
		{"rename", http.MethodPut, `{"root":"/","files":[{"from":"original","to":"renamed"}]}`},
		{"delete", http.MethodPost, `{"root":"/","files":["original"]}`},
		{"chmod", http.MethodPost, `{"root":"/","files":[{"file":"original","mode":"0600"}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newTransferHarness(t, &transferTestPanel{})
			s := h.seedServer()
			require.NoError(t, os.MkdirAll(s.Filesystem().Path(), 0o755))
			file := filepath.Join(s.Filesystem().Path(), "original")
			require.NoError(t, os.WriteFile(file, []byte("contents"), 0o644))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest(tt.method, "/api/servers/"+s.ID()+"/files/"+tt.name, strings.NewReader(tt.body)).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer test-token")
			res := httptest.NewRecorder()
			h.router.ServeHTTP(res, req)
			require.NotEqual(t, http.StatusNoContent, res.Code)
			contents, err := os.ReadFile(file)
			require.NoError(t, err)
			require.Equal(t, "contents", string(contents))
			stat, err := os.Stat(file)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o644), stat.Mode().Perm())
			require.NoFileExists(t, filepath.Join(s.Filesystem().Path(), "renamed"))
		})
	}
}

func TestOversizedJSONIsRejectedByProtectedRoutes(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	body := `{"action":"start"}` + strings.Repeat(" ", 4<<20)
	req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/power", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	res := httptest.NewRecorder()
	h.router.ServeHTTP(res, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, res.Code)
}
