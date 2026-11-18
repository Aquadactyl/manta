package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeAndLegacyConfigurationDefaults(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Linux installation defaults; macOS retains per-user directories")
	}
	for _, tc := range []struct {
		name, yaml, root, user, network string
	}{
		{"native", "config_version: 1\n", "/var/lib/aquadactyl", "aquadactyl", "aquadactyl_nw"},
		{"legacy omitted settings", "uuid: existing-node\n", "/var/lib/pterodactyl", "pterodactyl", "pterodactyl_nw"},
		{"legacy explicit custom path", "system:\n  root_directory: /srv/custom\n", "/srv/custom", "pterodactyl", "pterodactyl_nw"},
		{"native explicit custom path", "config_version: 1\nsystem:\n  root_directory: /srv/custom\n", "/srv/custom", "aquadactyl", "aquadactyl_nw"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.yml")
			require.NoError(t, os.WriteFile(p, []byte(tc.yaml), 0o600))
			c, err := Load(p)
			require.NoError(t, err)
			require.Equal(t, tc.root, c.System.RootDirectory)
			require.Equal(t, tc.user, c.System.Username)
			require.Equal(t, tc.network, c.Docker.Network.Name)
			// A save/reload must materialize the old defaults instead of switching data.
			require.NoError(t, WriteToDisk(c))
			saved, err := Load(p)
			require.NoError(t, err)
			require.Equal(t, c.System.RootDirectory, saved.System.RootDirectory)
			require.Equal(t, c.System.Data, saved.System.Data)
			require.Equal(t, c.System.LogDirectory, saved.System.LogDirectory)
			require.Equal(t, c.System.Username, saved.System.Username)
			require.Equal(t, c.System.Passwd.Directory, saved.System.Passwd.Directory)
			require.Equal(t, c.System.MachineID.Directory, saved.System.MachineID.Directory)
			require.Equal(t, c.Docker.Network, saved.Docker.Network)
			require.Equal(t, 1, saved.ConfigVersion)
		})
	}
}

func TestRejectFutureConfigurationDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yml")
	for _, yaml := range []string{"config_version: 2\n", "config_version: -1\n", "config_version: [invalid]\n"} {
		require.NoError(t, os.WriteFile(p, []byte(yaml), 0o600))
		_, err := Load(p)
		require.Error(t, err)
	}
}

func TestDatabasePathPreservesLegacyActivity(t *testing.T) {
	root := t.TempDir()
	native := filepath.Join(root, "manta.db")
	legacy := filepath.Join(root, "wings.db")
	require.Equal(t, native, ResolveDatabasePath(root))
	require.NoError(t, os.WriteFile(legacy, []byte("existing database"), 0o600))
	require.Equal(t, legacy, ResolveDatabasePath(root))
	require.NoError(t, os.Mkdir(native, 0o700))
	// Invalid canonical paths fail at open, never fall back to other activity data.
	require.Equal(t, native, ResolveDatabasePath(root))
}

func TestMantaTokenEnvironmentPrecedence(t *testing.T) {
	t.Setenv("MANTA_TOKEN_ID", "native-id")
	t.Setenv("MANTA_TOKEN", "native-token")
	t.Setenv("WINGS_TOKEN_ID", "legacy-id")
	t.Setenv("WINGS_TOKEN", "legacy-token")
	c, err := NewAtPath("unused")
	require.NoError(t, err)
	require.NoError(t, c.ResolveToken(false))
	require.Equal(t, "native-id", c.Token.ID)
	require.Equal(t, "native-token", c.Token.Token)
}
