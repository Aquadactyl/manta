package system

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogURLRemovesCredentialsAndSignedQueries(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{
		{"/upload/file?token=secret&token=another&directory=/uploads", "/upload/file"},
		{"/upload/file?%74oken=secret", "/upload/file"},
		{"https://user:password@files.example/file?X-Amz-Signature=secret#token", "https://files.example/file"},
		{"https://files.example/path%20with%20spaces/file", "https://files.example/path%20with%20spaces/file"},
		{"/file?token=bad%escape", "/file"},
		{"http://%secret", "(invalid URL)"},
	} {
		require.Equal(t, tt.want, LogURL(tt.raw))
	}
}
