//go:build darwin

package config

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDarwinDefaultsLiveInTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	c, err := NewAtPath(filepath.Join(home, "config.yml"))
	require.NoError(t, err)

	root := filepath.Join(home, "Library", "Application Support", "Manta")
	assert.Equal(t, root, c.System.RootDirectory)
	assert.Equal(t, filepath.Join(root, "volumes"), c.System.Data)
	assert.Equal(t, filepath.Join(root, "archives"), c.System.ArchiveDirectory)
	assert.Equal(t, filepath.Join(root, "backups"), c.System.BackupDirectory)
	assert.Equal(t, filepath.Join(root, "tmp"), c.System.TmpDirectory)
	assert.Equal(t, filepath.Join(home, "Library", "Logs", "Manta"), c.System.LogDirectory)
	assert.Equal(t, filepath.Join(root, "run", "etc"), c.System.Passwd.Directory)
	assert.Equal(t, filepath.Join(root, "run", "machine-id"), c.System.MachineID.Directory)
}

func TestDarwinConfigValuesTakePriorityOverDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := filepath.Join(home, "config.yml")
	require.NoError(t, os.WriteFile(p, []byte("system:\n  data: /Volumes/Games/volumes\n"), 0o600))

	c, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "/Volumes/Games/volumes", c.System.Data)
	assert.Equal(t, filepath.Join(home, "Library", "Application Support", "Manta", "archives"), c.System.ArchiveDirectory)
}

func TestDarwinEnsurePlatformUserUsesTheCurrentUser(t *testing.T) {
	u, err := user.Current()
	require.NoError(t, err)

	c, err := NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	c.AuthenticationToken = "test-token"
	Set(c)

	require.NoError(t, EnsurePterodactylUser())
	assert.Equal(t, u.Username, Get().System.Username)
	assert.Equal(t, u.Uid, strconv.Itoa(Get().System.User.Uid))
	assert.Equal(t, u.Gid, strconv.Itoa(Get().System.User.Gid))
}

func TestDarwinDetectPlatformTimezone(t *testing.T) {
	tz, ok := detectPlatformTimezone()
	if !ok {
		t.Skip("/etc/localtime does not point at a zone information file")
	}
	assert.False(t, strings.HasPrefix(tz, "/"))
	_, err := time.LoadLocation(tz)
	assert.NoError(t, err)
}

func TestDarwinPlatformFallbackBranches(t *testing.T) {
	oldHome := platformUserHomeDir
	platformUserHomeDir = func() (string, error) { return "", errors.New("home unavailable") }
	t.Cleanup(func() { platformUserHomeDir = oldHome })
	assert.Equal(t, "/var/lib/pterodactyl", supportDirectory())
	assert.Equal(t, "/var/log/pterodactyl", logDirectory())
	fallback := &Configuration{}
	applyPlatformDefaults(fallback)
	assert.Equal(t, "/var/lib/pterodactyl", fallback.System.RootDirectory)
	assert.Equal(t, "/var/log/pterodactyl", fallback.System.LogDirectory)
	platformUserHomeDir = func() (string, error) { return "", nil }
	assert.Equal(t, "/var/lib/pterodactyl", supportDirectory())
	assert.Equal(t, "/var/log/pterodactyl", logDirectory())

	oldEval := evalPlatformTimezoneTarget
	evalPlatformTimezoneTarget = func(string) (string, error) { return "", errors.New("timezone link unavailable") }
	t.Cleanup(func() { evalPlatformTimezoneTarget = oldEval })
	if _, ok := detectPlatformTimezone(); ok {
		t.Fatal("expected missing timezone link to be rejected")
	}
	evalPlatformTimezoneTarget = func(string) (string, error) { return "/usr/share/zoneinfo", nil }
	if _, ok := detectPlatformTimezone(); ok {
		t.Fatal("expected path without a zone name to be rejected")
	}
	evalPlatformTimezoneTarget = func(string) (string, error) { return "/usr/share/zoneinfo/Europe/Paris", nil }
	if zone, ok := detectPlatformTimezone(); !ok || zone != "Europe/Paris" {
		t.Fatalf("expected zone name, got %q (%v)", zone, ok)
	}
}

func TestDarwinPlatformUserLookupError(t *testing.T) {
	old := platformCurrentUser
	want := errors.New("user database unavailable")
	platformCurrentUser = func() (*user.User, error) { return nil, want }
	t.Cleanup(func() { platformCurrentUser = old })

	handled, err := ensurePlatformUser()
	assert.True(t, handled)
	assert.ErrorIs(t, err, want)
}

func TestDarwinOtherPlatformHelpers(t *testing.T) {
	old := readOSRelease
	readOSRelease = func() (map[string]string, error) { return map[string]string{"ID": "macos-test"}, nil }
	t.Cleanup(func() { readOSRelease = old })
	name, err := getSystemName()
	require.NoError(t, err)
	assert.Equal(t, "macos-test", name)

	assert.ErrorContains(t, probeOpenat2(), "only available on Linux")
}

func TestDarwinUserCommandHook(t *testing.T) {
	output, err := runUserCommand("/usr/bin/true")
	require.NoError(t, err)
	assert.Empty(t, output)
}
