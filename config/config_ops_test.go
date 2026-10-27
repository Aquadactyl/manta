package config

import (
	"os"
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

	// ConfigurePasswd enabled
	require.NoError(t, ConfigurePasswd())
	assert.FileExists(t, filepath.Join(cfg.System.Passwd.Directory, "group"))
	assert.FileExists(t, filepath.Join(cfg.System.Passwd.Directory, "passwd"))

	// ConfigurePasswd disabled
	cfg.System.Passwd.Enable = false
	require.NoError(t, ConfigurePasswd())

	// EnsurePterodactylUser in rootless mode
	cfg.System.User.Rootless.Enabled = true
	require.NoError(t, EnsurePterodactylUser())
	assert.NotEmpty(t, cfg.System.Username)

	// Expand
	t.Setenv("TEST_VAR", "expanded_val")
	expanded, err := Expand("value_${TEST_VAR}")
	require.NoError(t, err)
	assert.Equal(t, "value_expanded_val", expanded)

	testFilePath := filepath.Join(tmp, "token.txt")
	require.NoError(t, os.WriteFile(testFilePath, []byte("file_token_value\n"), 0644))
	fileExpanded, err := Expand("file://" + testFilePath)
	require.NoError(t, err)
	assert.Equal(t, "file_token_value", fileExpanded)

	_, err = Expand("file:///nonexistent/path/file.txt")
	assert.Error(t, err)

	// UseOpenat2
	openat2Set.Store(false)
	_ = UseOpenat2()
}

func TestConfigMoreOperations(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yml")

	cfg, err := NewAtPath(cfgPath)
	require.NoError(t, err)
	cfg.AuthenticationToken = "token-12345"
	cfg.System.RootDirectory = tmp
	Set(cfg)

	// 1. GetStatesPath
	assert.Equal(t, filepath.Join(tmp, "states.json"), cfg.System.GetStatesPath())

	// 2. Load and FromFile
	require.NoError(t, WriteToDisk(cfg))
	loaded, err := Load(cfgPath)
	require.NoError(t, err)
	assert.NotNil(t, loaded)

	_, err = Load(filepath.Join(tmp, "missing.yml"))
	assert.Error(t, err)

	require.NoError(t, FromFile(cfgPath))
	assert.Error(t, FromFile(filepath.Join(tmp, "missing.yml")))

	// 3. ConfigureDirectories with symlinked data dir
	realData := filepath.Join(tmp, "real_data")
	require.NoError(t, os.MkdirAll(realData, 0700))
	realData, err = filepath.EvalSymlinks(realData)
	require.NoError(t, err)
	symData := filepath.Join(tmp, "sym_data")
	require.NoError(t, os.Symlink(realData, symData))

	cfg.System.Data = symData
	Set(cfg)
	require.NoError(t, ConfigureDirectories())
	assert.Equal(t, realData, Get().System.Data)

	// 4. EnableLogRotation disabled
	cfg.System.EnableLogRotate = false
	Set(cfg)
	assert.NoError(t, EnableLogRotation())

	// 5. ConfigureTimezone with TZ env var
	t.Setenv("TZ", "America/New_York")
	cfg.System.Timezone = ""
	Set(cfg)
	require.NoError(t, ConfigureTimezone())
	assert.Equal(t, "America/New_York", Get().System.Timezone)

	// 6. RegistryCredentials
	cfg.Docker.Registries = map[string]RegistryConfiguration{
		"ghcr.io": {
			Username: "user",
			Password: "password",
		},
	}
	reg, creds := cfg.Docker.RegistryCredentialsForImage("ghcr.io/pterodactyl/installers:alpine")
	assert.NotEmpty(t, reg)
	assert.NotNil(t, creds)
	assert.Equal(t, "user", creds.Username)
	b64, err := creds.Base64()
	assert.NoError(t, err)
	assert.NotEmpty(t, b64)

	_, credsNil := cfg.Docker.RegistryCredentialsForImage("nginx:latest")
	assert.Nil(t, credsNil)

	// 7. EnsurePterodactylUser with rootless
	cfg.System.User.Rootless.Enabled = true
	Set(cfg)
	assert.NoError(t, EnsurePterodactylUser())
	assert.NotEmpty(t, Get().System.Username)

	// 8. EnsurePterodactylUser with root user
	cfg.System.User.Rootless.Enabled = false
	cfg.System.Username = "root"
	Set(cfg)
	assert.NoError(t, EnsurePterodactylUser())
	assert.Equal(t, 0, Get().System.User.Uid)

	// 9. ConfigureTimezone invalid
	cfg.System.Timezone = "Invalid/Zone/Nonexistent"
	Set(cfg)
	assert.Error(t, ConfigureTimezone())

	// 10. getSystemName and UseOpenat2
	name, _ := getSystemName()
	assert.NotEmpty(t, name)
	_ = UseOpenat2()

	// 11. StatesPath and LogRotation
	assert.Equal(t, filepath.Join(cfg.System.RootDirectory, "states.json"), cfg.System.GetStatesPath())
	cfg.System.EnableLogRotate = true
	Set(cfg)
	assert.NoError(t, EnableLogRotation())

	// 12. UseOpenat2 modes
	openat2Set.Store(false)
	cfg.System.OpenatMode = "openat2"
	Set(cfg)
	assert.True(t, UseOpenat2())

	openat2Set.Store(false)
	cfg.System.OpenatMode = "openat"
	Set(cfg)
	assert.False(t, UseOpenat2())

	openat2Set.Store(false)
	cfg.System.OpenatMode = ""
	Set(cfg)
	_ = UseOpenat2()

	// 13. parseDockerRegistryReference edge cases
	dom, _, ok := parseDockerRegistryReference("")
	assert.False(t, ok)
	assert.Empty(t, dom)

	dom, pth, ok := parseDockerRegistryReference("https://index.docker.io/v1/")
	assert.True(t, ok)
	assert.Equal(t, "docker.io", dom)
	assert.Empty(t, pth)

	dom, pth, ok = parseDockerRegistryReference("https://myregistry.com/v2")
	assert.True(t, ok)
	assert.Equal(t, "myregistry.com", dom)
	assert.Empty(t, pth)

	dom, pth, ok = parseDockerRegistryReference("https://myregistry.com/custom/team")
	assert.True(t, ok)
	assert.Equal(t, "myregistry.com", dom)
	assert.Equal(t, "custom/team", pth)

	_, _, ok = parseDockerRegistryReference("///")
	assert.False(t, ok)
}

func TestEnsurePterodactylUserAndPasswd(t *testing.T) {
	orig := Get()
	defer Set(orig)

	// 1. ConfigurePasswd disabled
	cfg := *orig
	cfg.System.Passwd.Enable = false
	Set(&cfg)
	assert.NoError(t, ConfigurePasswd())

	// 2. ConfigurePasswd enabled
	tmp := t.TempDir()
	cfg.System.Passwd.Enable = true
	cfg.System.Passwd.Directory = tmp
	cfg.System.User.Uid = 988
	cfg.System.User.Gid = 988
	Set(&cfg)
	require.NoError(t, ConfigurePasswd())
	assert.FileExists(t, filepath.Join(tmp, "group"))
	assert.FileExists(t, filepath.Join(tmp, "passwd"))

	// 3. EnsurePterodactylUser rootless mode
	cfg.System.User.Rootless.Enabled = true
	Set(&cfg)
	require.NoError(t, EnsurePterodactylUser())
	assert.NotEmpty(t, Get().System.Username)

	// 4. EnsurePterodactylUser with existing root user
	cfg.System.User.Rootless.Enabled = false
	cfg.System.Username = "root"
	Set(&cfg)
	require.NoError(t, EnsurePterodactylUser())
	assert.Equal(t, 0, Get().System.User.Uid)
	assert.Equal(t, 0, Get().System.User.Gid)

	// 5. Load tests
	loaded, err := Load(filepath.Join(tmp, "passwd"))
	assert.Error(t, err) // invalid yaml
	assert.Nil(t, loaded)

	_, err = Load(filepath.Join(tmp, "non-existent.yml"))
	assert.Error(t, err)

	validYaml := filepath.Join(tmp, "valid.yml")
	require.NoError(t, os.WriteFile(validYaml, []byte("debug: true\n"), 0644))
	loaded, err = Load(validYaml)
	require.NoError(t, err)
	assert.True(t, loaded.Debug)

	// 6. ConfigureTimezone tests
	cfg.System.Timezone = "UTC"
	Set(&cfg)
	assert.NoError(t, ConfigureTimezone())

	cfg.System.Timezone = "America/New_York"
	Set(&cfg)
	assert.NoError(t, ConfigureTimezone())

	cfg.System.Timezone = "Invalid/Timezone"
	Set(&cfg)
	assert.Error(t, ConfigureTimezone())

	cfg.System.Timezone = ""
	t.Setenv("TZ", "Europe/London")
	Set(&cfg)
	assert.NoError(t, ConfigureTimezone())
	assert.Equal(t, "Europe/London", Get().System.Timezone)
}



