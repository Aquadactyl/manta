//go:build darwin

package config

import (
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
