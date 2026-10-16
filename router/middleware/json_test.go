package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBindJSONBoundsAndValidatesRequests(t *testing.T) {
	const prefix, suffix = `{"value":"`, `"}`
	atLimit := prefix + strings.Repeat("a", int(MaxJSONRequestSize)-len(prefix)-len(suffix)) + suffix
	tests := []struct {
		name    string
		body    string
		chunked bool
		status  int
	}{
		{"valid", `{"value":"ok"}`, false, http.StatusNoContent},
		{"at limit", atLimit, false, http.StatusNoContent},
		{"known oversized body", atLimit + " ", false, http.StatusRequestEntityTooLarge},
		{"chunked oversized body", atLimit + " ", true, http.StatusRequestEntityTooLarge},
		{"oversized suffix", `{"value":"ok"}` + strings.Repeat(" ", int(MaxJSONRequestSize)), true, http.StatusRequestEntityTooLarge},
		{"second document", `{"value":"ok"} {}`, true, http.StatusBadRequest},
		{"malformed", `{"value":`, false, http.StatusBadRequest},
		{"validation failure", `{}`, false, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.ReleaseMode)
			e := gin.New()
			var applied bool
			e.POST("/control", func(c *gin.Context) {
				var data struct {
					Value string `json:"value" binding:"required"`
				}
				if err := BindJSON(c, &data); err != nil {
					return
				}
				applied = true
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/control", strings.NewReader(tt.body))
			if tt.chunked {
				req.ContentLength = -1
				req.TransferEncoding = []string{"chunked"}
			}
			res := httptest.NewRecorder()
			e.ServeHTTP(res, req)
			require.Equal(t, tt.status, res.Code)
			require.Equal(t, tt.status == http.StatusNoContent, applied)
		})
	}
}
