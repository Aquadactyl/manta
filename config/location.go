package config

import (
	"errors"
	"os"
	"path/filepath"
)

// ResolveDatabasePath keeps existing activity data without moving an open database.
// Fresh nodes use manta.db; existing databases remain at their original location.
func ResolveDatabasePath(root string) string {
	return existingConfigPath(filepath.Join(root, "manta.db"), []string{filepath.Join(root, "wings.db")})
}

// ResolveConfigPath discovers an existing configuration only when the caller
// has not explicitly selected a path. New installations use DefaultLocation.
func ResolveConfigPath(requested string, explicit bool) string {
	if explicit || requested != DefaultLocation {
		return requested
	}
	return existingConfigPath(requested, legacyConfigLocations())
}

func existingConfigPath(preferred string, legacy []string) string {
	// Lstat deliberately keeps a dangling symlink, unreadable path or directory
	// selected. Loading it must report its error rather than use another node's
	// credentials from a fallback configuration.
	if _, err := os.Lstat(preferred); !errors.Is(err, os.ErrNotExist) {
		return preferred
	}
	for _, candidate := range legacy {
		if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
	return preferred
}
