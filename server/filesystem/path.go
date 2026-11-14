package filesystem

import (
	"path"
	"path/filepath"
	"strings"

	"emperror.dev/errors"
)

// IsIgnored checks whether a path is in the server file denylist. If so, an Error
// is returned, otherwise nil is returned.
func (fs *Filesystem) IsIgnored(paths ...string) error {
	for _, p := range paths {
		// Match against the cleaned path the filesystem will use. A trailing slash is
		// kept so that it still matches patterns for directories.
		cleaned := path.Clean("/" + filepath.ToSlash(p))
		if strings.HasSuffix(p, "/") && cleaned != "/" {
			cleaned += "/"
		}
		if fs.denylist.MatchesPath(cleaned) {
			return errors.WithStack(&Error{code: ErrCodeDenylistFile, path: p, resolved: p})
		}
	}
	return nil
}
