//go:build !darwin

package cmd

import (
	"io"

	"github.com/NYTimes/logrotate"
)

// openLogFile opens the log file. It is reopened when the process receives
// SIGHUP, which is how the logrotate configuration written on startup rotates it.
func openLogFile(p string) (io.Writer, error) {
	return logrotate.NewFile(p)
}
