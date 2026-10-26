package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigOperations(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yml")

	// 1. NewAtPath
	cfg, err := NewAtPath(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, cfgPath, cfg.path)

	cfg.AuthenticationToken = "test-token-12345"
	cfg.AuthenticationTokenId = "test-id-12345"
	cfg.Token = Token{ID: "test-id-12345", Token: "test-token-12345"}
	cfg.System.RootDirectory = tmp

	// 2. Set and Get
	Set(cfg)
	got := Get()
	assert.Equal(t, "test-token-12345", got.Token.Token)

	// 3. GetToken
	tok := GetToken()
	assert.Equal(t, "test-id-12345", tok.ID)
	assert.Equal(t, "test-token-12345", tok.Token)

	// 4. Update
	Update(func(c *Configuration) {
		c.Api.Port = 8085
	})
	assert.Equal(t, 8085, Get().Api.Port)

	// 5. SetDebugViaFlag
	SetDebugViaFlag(true)
	assert.True(t, Get().Debug)

	// 6. GetJwtAlgorithm
	algo := GetJwtAlgorithm()
	assert.NotNil(t, algo)

	// 7. WriteToDisk
	require.NoError(t, WriteToDisk(cfg))
	assert.FileExists(t, cfgPath)

	// 8. FromFile
	require.NoError(t, FromFile(cfgPath))
	assert.Equal(t, 8085, Get().Api.Port)

	// 9. WriteToDisk with no path returns error
	noPathCfg := &Configuration{}
	assert.Error(t, WriteToDisk(noPathCfg))

	// 10. StatesPath
	assert.Equal(t, filepath.Join(tmp, "states.json"), cfg.System.GetStatesPath())
}

func TestPanelConfiguration(t *testing.T) {
	cfg := &Configuration{
		Debug:                 false,
		Uuid:                  "test-uuid-123",
		AuthenticationTokenId: "id-1",
		AuthenticationToken:   "tok-1",
	}
	cfg.Api.Host = "127.0.0.1"
	cfg.Api.Port = 9000
	cfg.Api.UploadLimit = 50
	cfg.System.Sftp.Port = 2022

	panelCfg := cfg.PanelConfiguration()
	assert.Equal(t, "test-uuid-123", panelCfg.Uuid)
	assert.Equal(t, "id-1", panelCfg.TokenId)
	assert.Equal(t, "tok-1", panelCfg.Token)
	assert.Equal(t, 9000, panelCfg.Api.Port)
	assert.Equal(t, 2022, panelCfg.System.Sftp.Port)

	newCfg := &Configuration{}
	newCfg.ApplyPanelConfiguration(panelCfg)
	assert.Equal(t, "test-uuid-123", newCfg.Uuid)
	assert.Equal(t, "id-1", newCfg.AuthenticationTokenId)
	assert.Equal(t, "tok-1", newCfg.AuthenticationToken)
	assert.Equal(t, 9000, newCfg.Api.Port)
	assert.Equal(t, 2022, newCfg.System.Sftp.Port)
}

func TestDockerRegistryAndOverhead(t *testing.T) {
	// Base64 on RegistryConfiguration
	reg := RegistryConfiguration{
		Username: "admin",
		Password: "secret-password",
	}
	b64, err := reg.Base64()
	require.NoError(t, err)
	assert.NotEmpty(t, b64)

	// Overhead.GetMultiplier default
	ohDefault := Overhead{Override: false}
	assert.Equal(t, 1.15, ohDefault.GetMultiplier(1024))
	assert.Equal(t, 1.10, ohDefault.GetMultiplier(3000))
	assert.Equal(t, 1.05, ohDefault.GetMultiplier(8192))

	// Overhead.GetMultiplier with override
	ohOverride := Overhead{
		Override:          true,
		DefaultMultiplier: 1.08,
		Multipliers: map[int]float64{
			1024: 1.25,
			2048: 1.20,
		},
	}
	assert.Equal(t, 1.25, ohOverride.GetMultiplier(1000))
	assert.Equal(t, 1.20, ohOverride.GetMultiplier(2000))
	assert.Equal(t, 1.08, ohOverride.GetMultiplier(5000))

	// Docker ContainerLogConfig
	dcfgEmpty := DockerConfiguration{}
	assert.Empty(t, dcfgEmpty.ContainerLogConfig().Type)

	var dcfgSet DockerConfiguration
	dcfgSet.LogConfig.Type = "local"
	dcfgSet.LogConfig.Config = map[string]string{"max-size": "10m"}
	assert.Equal(t, "local", dcfgSet.ContainerLogConfig().Type)
	assert.Equal(t, "10m", dcfgSet.ContainerLogConfig().Config["max-size"])
}

func TestConfigureSystemDirectoriesAndTimezone(t *testing.T) {
	tmp := t.TempDir()
	cfg := &Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	cfg.System.RootDirectory = filepath.Join(tmp, "root")
	cfg.System.Data = filepath.Join(tmp, "data")
	cfg.System.TmpDirectory = filepath.Join(tmp, "tmp")
	cfg.System.ArchiveDirectory = filepath.Join(tmp, "archive")
	cfg.System.BackupDirectory = filepath.Join(tmp, "backup")
	cfg.System.Passwd.Enable = true
	cfg.System.Passwd.Directory = filepath.Join(tmp, "passwd")
	cfg.System.MachineID.Enable = true
	cfg.System.MachineID.Directory = filepath.Join(tmp, "machine-id")
	cfg.System.EnableLogRotate = false
	cfg.System.Timezone = "UTC"
	Set(cfg)

	require.NoError(t, ConfigureDirectories())
	assert.DirExists(t, cfg.System.RootDirectory)
	assert.DirExists(t, cfg.System.Data)
	assert.DirExists(t, cfg.System.TmpDirectory)
	assert.DirExists(t, cfg.System.ArchiveDirectory)
	assert.DirExists(t, cfg.System.BackupDirectory)
	assert.DirExists(t, cfg.System.Passwd.Directory)
	assert.DirExists(t, cfg.System.MachineID.Directory)

	require.NoError(t, EnableLogRotation())
	require.NoError(t, ConfigureTimezone())
}
