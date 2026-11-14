// SPDX-License-Identifier: MIT

//go:build darwin

package config

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/apex/log"

	"github.com/pterodactyl/wings/system"
)

// Manta on macOS keeps its files in the home directory of the user it runs as,
// which is where launchd agents, Docker Desktop file sharing, and the
// permissions of an unprivileged user all agree. The system directories used on
// Linux are read-only, or not shared with the virtual machine that runs the
// containers.

// supportDirectory returns the directory holding the configuration and data.
func supportDirectory() string {
	home, err := platformUserHomeDir()
	if err != nil || home == "" {
		// Without a home directory nothing sensible can be derived, so keep the
		// location every other platform uses.
		return "/var/lib/pterodactyl"
	}
	return filepath.Join(home, "Library", "Application Support", "Manta")
}

// logDirectory returns the directory holding the log files.
func logDirectory() string {
	home, err := platformUserHomeDir()
	if err != nil || home == "" {
		return "/var/log/pterodactyl"
	}
	return filepath.Join(home, "Library", "Logs", "Manta")
}

// DefaultLocation is the location of the configuration file when none is given.
var DefaultLocation = filepath.Join(supportDirectory(), "config.yml")

// applyPlatformDefaults replaces the defaults that only make sense on Linux.
func applyPlatformDefaults(c *Configuration) {
	root := supportDirectory()
	c.System.RootDirectory = root
	c.System.LogDirectory = logDirectory()
	c.System.Data = filepath.Join(root, "volumes")
	c.System.ArchiveDirectory = filepath.Join(root, "archives")
	c.System.BackupDirectory = filepath.Join(root, "backups")
	// The temporary directory of the system is cleaned up automatically, which
	// must not happen to a transfer that is still in progress.
	c.System.TmpDirectory = filepath.Join(root, "tmp")
	// There is no /run on macOS.
	c.System.Passwd.Directory = filepath.Join(root, "run", "etc")
	c.System.MachineID.Directory = filepath.Join(root, "run", "machine-id")
}

// ensurePlatformUser makes servers run as the user Manta runs as. Containers
// run in a virtual machine and reach the files through file sharing that
// presents them as owned by that user, so no separate system user is created,
// and none could be as Manta does not run as root.
func ensurePlatformUser() (bool, error) {
	u, err := platformCurrentUser()
	if err != nil {
		return true, err
	}
	log.WithField("username", u.Username).Info("running on macOS, using the current user for servers and skipping user creation")
	_config.System.Username = u.Username
	_config.System.User.Uid = system.MustInt(u.Uid)
	_config.System.User.Gid = system.MustInt(u.Gid)
	return true, nil
}

// detectPlatformTimezone reads the timezone from the link macOS keeps to the
// zone information file in use.
func detectPlatformTimezone() (string, bool) {
	target, err := evalPlatformTimezoneTarget("/etc/localtime")
	if err != nil {
		return "", false
	}
	// The target is something like /var/db/timezone/zoneinfo/Europe/Berlin.
	_, zone, ok := strings.Cut(filepath.ToSlash(target), "zoneinfo/")
	if !ok || zone == "" {
		return "", false
	}
	return zone, true
}

var (
	platformUserHomeDir        = os.UserHomeDir
	platformCurrentUser        = user.Current
	evalPlatformTimezoneTarget = filepath.EvalSymlinks
)
