package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExistingConfigPathSelection(t *testing.T) {
	for _, scenario := range []string{"fresh", "legacy only", "both", "canonical directory", "canonical dangling link", "invalid canonical parent"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			preferred := filepath.Join(dir, "aquadactyl", "config.yml")
			legacy := filepath.Join(dir, "legacy.yml")
			want := preferred
			if scenario != "fresh" {
				require.NoError(t, os.WriteFile(legacy, []byte("legacy"), 0o600))
			}
			switch scenario {
			case "legacy only":
				want = legacy
			case "both":
				require.NoError(t, os.MkdirAll(filepath.Dir(preferred), 0o700))
				require.NoError(t, os.WriteFile(preferred, []byte("canonical"), 0o600))
			case "canonical directory":
				require.NoError(t, os.MkdirAll(preferred, 0o700))
			case "canonical dangling link":
				require.NoError(t, os.MkdirAll(filepath.Dir(preferred), 0o700))
				require.NoError(t, os.Symlink(filepath.Join(dir, "missing.yml"), preferred))
			case "invalid canonical parent":
				require.NoError(t, os.WriteFile(filepath.Dir(preferred), []byte("not a directory"), 0o600))
			}
			require.Equal(t, want, existingConfigPath(preferred, []string{legacy}))
		})
	}
}

func TestResolveConfigPathHonorsExplicitAndCustomPaths(t *testing.T) {
	require.Equal(t, DefaultLocation, ResolveConfigPath(DefaultLocation, true))
	custom := filepath.Join(t.TempDir(), "operator.yml")
	require.Equal(t, custom, ResolveConfigPath(custom, false))
	require.Equal(t, custom, ResolveConfigPath(custom, true))
}
