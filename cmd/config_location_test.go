package cmd

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/Aquadactyl/manta/config"
)

func TestStartupConfigurationDiscoveryHonorsConfigFlag(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit default", true: "explicit default"}[explicit], func(t *testing.T) {
			oldPath, oldResolver, oldDebug := configPath, resolveConfigPath, debug
			t.Cleanup(func() {
				configPath, resolveConfigPath, debug = oldPath, oldResolver, oldDebug
				config.Set(&config.Configuration{})
			})
			target := filepath.Join(t.TempDir(), "existing.yml")
			cfg, err := config.NewAtPath(target)
			require.NoError(t, err)
			cfg.AppName = "existing configuration"
			cfg.System.Data = filepath.Join(t.TempDir(), "custom-server-data")
			require.NoError(t, config.WriteToDisk(cfg))
			configPath, debug = config.DefaultLocation, false
			resolveConfigPath = func(requested string, wasExplicit bool) string {
				require.Equal(t, config.DefaultLocation, requested)
				require.Equal(t, explicit, wasExplicit)
				return target
			}
			command := &cobra.Command{}
			command.Flags().String("config", config.DefaultLocation, "")
			if explicit {
				require.NoError(t, command.Flags().Set("config", config.DefaultLocation))
			}
			initConfig(command)
			require.Equal(t, target, configPath)
			require.Equal(t, "existing configuration", config.Get().AppName)
			require.Equal(t, cfg.System.Data, config.Get().System.Data)
		})
	}
}

func TestConfigureDiscoveryPreservesExistingLocalSettings(t *testing.T) {
	clearTokenEnvironment(t)
	target := filepath.Join(t.TempDir(), "legacy.yml")
	cfg, err := config.NewAtPath(target)
	require.NoError(t, err)
	cfg.System.Data = filepath.Join(t.TempDir(), "custom-data")
	require.NoError(t, config.WriteToDisk(cfg))
	panel := newFakePanel(t, `{"token_id":"updated-id","token":"updated-token","system":{"data":"/stale-panel-path"}}`)
	setupConfigureRun(t, panel.URL, config.DefaultLocation)
	configureArgs.Override = true
	oldResolver := resolveConfigPath
	t.Cleanup(func() { resolveConfigPath = oldResolver })
	resolveConfigPath = func(requested string, explicit bool) string {
		require.Equal(t, config.DefaultLocation, requested)
		require.False(t, explicit)
		return target
	}
	configureCmdRun(nil, nil)
	updated, err := config.Load(target)
	require.NoError(t, err)
	require.Equal(t, cfg.System.Data, updated.System.Data)
	require.Equal(t, "updated-token", updated.AuthenticationToken)
}
