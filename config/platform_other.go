// SPDX-License-Identifier: MIT

//go:build !darwin

package config

// DefaultLocation is the location of the configuration file when none is given.
const DefaultLocation = "/etc/pterodactyl/config.yml"

// applyPlatformDefaults replaces defaults that are specific to a platform. The
// defaults declared on the configuration are already correct for Linux.
func applyPlatformDefaults(_ *Configuration) {}

// ensurePlatformUser handles the system user on platforms that need something
// other than the default behavior. It reports whether it did so.
func ensurePlatformUser() (bool, error) {
	handled := false
	var err error
	return handled, err
}

// detectPlatformTimezone returns the timezone of the system on platforms where
// it can be read without the files and tools used by default.
func detectPlatformTimezone() (string, bool) {
	timezone := ""
	configured := false
	return timezone, configured
}
