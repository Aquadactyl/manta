//go:build darwin

package cmd

import (
	"io"

	"github.com/NYTimes/logrotate"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/rotatinglog"
)

const (
	// logMaxSize is the size at which the log file is rotated.
	logMaxSize = 10 << 20

	// logKeep is the number of rotated log files that are kept.
	logKeep = 7
)

// openLogFile opens the log file. macOS has no logrotate, so the file rotates
// itself by size unless system.enable_log_rotate is turned off, in which case it
// is reopened on SIGHUP for whatever rotates it from the outside.
func openLogFile(p string) (io.Writer, error) {
	if !config.Get().System.EnableLogRotate {
		return logrotate.NewFile(p)
	}
	return rotatinglog.Open(p, logMaxSize, logKeep)
}
