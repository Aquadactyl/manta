package sftp

import (
	"strings"
	"testing"
)

func TestSFTPUsernameSupportsPanelIdentifiers(t *testing.T) {
	for _, username := range []string{
		"admin.fe109884", "user.with.dots.fe109884",
		"admin.fe109884-f53a-45de-9c33-e17677b52fd4",
		"admin.serv_" + strings.Repeat("a", 26),
	} {
		if !validUsernameRegexp.MatchString(username) {
			t.Errorf("valid username rejected: %s", username)
		}
	}
	for _, username := range []string{
		".fe109884", "admin.", "admin.fe10988", "admin.fe1098840",
		"admin.serv_" + strings.Repeat("a", 25), "admin.serv_" + strings.Repeat("1", 26),
		"admin.ze109884-f53a-45de-9c33-e17677b52fd4", "admin.../../etc/passwd",
	} {
		if validUsernameRegexp.MatchString(username) {
			t.Errorf("invalid username accepted: %s", username)
		}
	}
}
