package system

import "net/url"

// LogURL keeps a URL's destination and path while removing credentials,
// signed query strings, and fragments before it is logged.
func LogURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid URL)"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}
