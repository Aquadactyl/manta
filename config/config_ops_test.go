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
	cfg.System.LogDirectory = filepath.Join(tmp, "logs")
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

	// Symlinked data directory
	realData := filepath.Join(tmp, "real_data")
	require.NoError(t, os.MkdirAll(realData, 0700))
	symData := filepath.Join(tmp, "sym_data")
	require.NoError(t, os.Symlink(realData, symData))
	cfg.System.Data = symData
	Set(cfg)
	require.NoError(t, ConfigureDirectories())
	assert.Equal(t, realData, Get().System.Data)

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
	cfg.System.RootDirectory = filepath.Join(tmp, "root")
	cfg.System.Data = filepath.Join(tmp, "data")
	cfg.System.LogDirectory = filepath.Join(tmp, "logs")
	cfg.System.TmpDirectory = filepath.Join(tmp, "tmp")
	cfg.System.ArchiveDirectory = filepath.Join(tmp, "archive")
	cfg.System.BackupDirectory = filepath.Join(tmp, "backup")
	cfg.System.Passwd.Enable = false
	cfg.System.MachineID.Enable = false
	Set(cfg)

	// 1. GetStatesPath
	assert.Equal(t, filepath.Join(tmp, "root", "states.json"), cfg.System.GetStatesPath())

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
	logrotateDir := t.TempDir()
	origLogrotate := logrotateDirectory
	logrotateDirectory = logrotateDir
	defer func() { logrotateDirectory = origLogrotate }()
	assert.NoError(t, EnableLogRotation())
	assert.FileExists(t, filepath.Join(logrotateDir, "wings"))
	// Second run returns nil when file already exists
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
	var orig *Configuration
	if _config != nil {
		c := *_config
		orig = &c
		defer Set(orig)
	} else {
		orig = &Configuration{AuthenticationToken: "test"}
		Set(orig)
	}

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

	// 8. ConfigureTimezone when Timezone is empty and TZ is empty
	cfg.System.Timezone = ""
	t.Setenv("TZ", "")
	Set(&cfg)
	_ = ConfigureTimezone()

	// 9. ResolveToken when env has invalid file path
	t.Setenv("WINGS_TOKEN_ID", "file:///invalid/nonexistent/secret/path/to/token")
	cfg.AuthenticationTokenId = "test"
	assert.Error(t, cfg.ResolveToken(false))
	t.Setenv("WINGS_TOKEN_ID", "")
}

func TestResolveTokenRemoteAndEdgeCases(t *testing.T) {
	// Remote cannot use $ indirection
	cfgInd1 := Configuration{AuthenticationToken: "$SECRET_TOKEN"}
	assert.Error(t, cfgInd1.ResolveToken(true))

	// Remote cannot use file:// indirection
	cfgInd2 := Configuration{AuthenticationToken: "file:///tmp/token"}
	assert.Error(t, cfgInd2.ResolveToken(true))

	// Remote mismatched environment override
	t.Setenv("WINGS_TOKEN", "env-token")
	cfgMismatch := Configuration{AuthenticationToken: "diff-token"}
	assert.Error(t, cfgMismatch.ResolveToken(true))

	// Remote matching environment override
	cfgMatch := Configuration{AuthenticationToken: "env-token"}
	assert.NoError(t, cfgMatch.ResolveToken(true))
	assert.Equal(t, "env-token", cfgMatch.Token.Token)
	t.Setenv("WINGS_TOKEN", "")

	// Remote without environment override returns local
	cfgLocal := Configuration{AuthenticationToken: "my-local-token"}
	assert.NoError(t, cfgLocal.ResolveToken(true))
	assert.Equal(t, "my-local-token", cfgLocal.Token.Token)
}

func TestSetDebugViaFlagAndWriteToDisk(t *testing.T) {
	orig := _config
	if orig != nil {
		defer Set(orig)
	} else {
		orig = &Configuration{AuthenticationToken: "test-token"}
		defer Set(orig)
	}

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yml")
	cfg := *orig
	cfg.AuthenticationToken = "test-token"
	cfg.path = cfgPath
	cfg.Debug = true
	Set(&cfg)

	SetDebugViaFlag(true)
	defer SetDebugViaFlag(false)

	require.NoError(t, WriteToDisk(&cfg))
	content, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "debug: false")
}

func TestConfigureDirectoriesBranches(t *testing.T) {
	orig := _config
	if orig != nil {
		defer Set(orig)
	} else {
		orig = &Configuration{AuthenticationToken: "test"}
	}

	tmp := t.TempDir()
	cfg := *orig
	cfg.System.RootDirectory = filepath.Join(tmp, "root")
	cfg.System.Data = filepath.Join(tmp, "data")
	cfg.System.LogDirectory = filepath.Join(tmp, "logs")
	cfg.System.TmpDirectory = filepath.Join(tmp, "tmp")
	cfg.System.ArchiveDirectory = filepath.Join(tmp, "archive")
	cfg.System.BackupDirectory = filepath.Join(tmp, "backup")
	cfg.System.Passwd.Enable = true
	cfg.System.Passwd.Directory = filepath.Join(tmp, "passwd")
	cfg.System.MachineID.Enable = true
	cfg.System.MachineID.Directory = filepath.Join(tmp, "machine-id")
	Set(&cfg)

	require.NoError(t, ConfigureDirectories())
	assert.DirExists(t, cfg.System.Data)
	assert.DirExists(t, cfg.System.TmpDirectory)
	assert.DirExists(t, cfg.System.ArchiveDirectory)
	assert.DirExists(t, cfg.System.BackupDirectory)
	assert.DirExists(t, cfg.System.Passwd.Directory)
	assert.DirExists(t, cfg.System.MachineID.Directory)

	// Error when directory is blocked by a file
	blocker := filepath.Join(tmp, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("file"), 0644))
	cfg.System.Data = filepath.Join(blocker, "sub")
	Set(&cfg)
	assert.Error(t, ConfigureDirectories())

	// WriteToDisk error when path is invalid
	badCfg := *orig
	badCfg.path = filepath.Join(blocker, "config.yml")
	assert.Error(t, WriteToDisk(&badCfg))

	// ConfigurePasswd error when path is blocked
	cfg.System.Passwd.Directory = blocker
	Set(&cfg)
	assert.Error(t, ConfigurePasswd())
}

func TestConfigRemainingBranches(t *testing.T) {
	orig := _config
	if orig != nil {
		c := *orig
		defer Set(&c)
	} else {
		orig = &Configuration{AuthenticationToken: "test"}
		defer Set(orig)
	}

	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "blocker_file")
	require.NoError(t, os.WriteFile(blocker, []byte("file"), 0o644))

	baseCfg := func() Configuration {
		c := *orig
		c.System.RootDirectory = filepath.Join(tmp, "root")
		c.System.Data = filepath.Join(tmp, "data")
		c.System.TmpDirectory = filepath.Join(tmp, "tmp")
		c.System.ArchiveDirectory = filepath.Join(tmp, "archive")
		c.System.BackupDirectory = filepath.Join(tmp, "backup")
		c.System.Passwd.Enable = true
		c.System.Passwd.Directory = filepath.Join(tmp, "passwd")
		c.System.MachineID.Enable = true
		c.System.MachineID.Directory = filepath.Join(tmp, "machine-id")
		return c
	}

	// 1. Each MkdirAll failure in ConfigureDirectories
	c1 := baseCfg()
	c1.System.RootDirectory = blocker
	Set(&c1)
	assert.Error(t, ConfigureDirectories())

	c2 := baseCfg()
	c2.System.Data = blocker
	Set(&c2)
	assert.Error(t, ConfigureDirectories())

	c3 := baseCfg()
	c3.System.TmpDirectory = blocker
	Set(&c3)
	assert.Error(t, ConfigureDirectories())

	c4 := baseCfg()
	c4.System.ArchiveDirectory = blocker
	Set(&c4)
	assert.Error(t, ConfigureDirectories())

	c5 := baseCfg()
	c5.System.BackupDirectory = blocker
	Set(&c5)
	assert.Error(t, ConfigureDirectories())

	c6 := baseCfg()
	c6.System.Passwd.Directory = blocker
	Set(&c6)
	assert.Error(t, ConfigureDirectories())

	c7 := baseCfg()
	c7.System.MachineID.Directory = blocker
	Set(&c7)
	assert.Error(t, ConfigureDirectories())

	// 2. ConfigurePasswd when group succeeds but passwd is a directory
	passwdDir := filepath.Join(tmp, "passwd_err_dir")
	require.NoError(t, os.MkdirAll(filepath.Join(passwdDir, "passwd"), 0o755))
	cpCfg := baseCfg()
	cpCfg.System.Passwd.Directory = passwdDir
	Set(&cpCfg)
	assert.Error(t, ConfigurePasswd())

	// 3. FromFile when ResolveToken fails
	badTokenCfgPath := filepath.Join(tmp, "bad_token.yml")
	require.NoError(t, os.WriteFile(badTokenCfgPath, []byte("token: file:///nonexistent/token/file\n"), 0o644))
	assert.Error(t, FromFile(badTokenCfgPath))

	// 4. EnableLogRotation branches
	origLogrotate := logrotateDirectory
	defer func() { logrotateDirectory = origLogrotate }()

	lrCfg := baseCfg()
	lrCfg.System.EnableLogRotate = true
	Set(&lrCfg)

	// Stat error != ErrNotExist
	logrotateDirectory = filepath.Join(blocker, "sub")
	assert.Error(t, EnableLogRotation())

	// Not a directory
	logrotateDirectory = blocker
	assert.NoError(t, EnableLogRotation())

	// Does not exist
	logrotateDirectory = filepath.Join(tmp, "does_not_exist_lr")
	assert.NoError(t, EnableLogRotation())

	// os.Create failure via dangling symlink into nonexistent parent
	lrValidDir := filepath.Join(tmp, "lr_valid")
	require.NoError(t, os.MkdirAll(lrValidDir, 0o755))
	require.NoError(t, os.Symlink(filepath.Join(tmp, "missing_parent", "wings"), filepath.Join(lrValidDir, "wings")))
	logrotateDirectory = lrValidDir
	assert.Error(t, EnableLogRotation())

	// 5. EnsurePterodactylUser with non-existent user and fake useradd in PATH
	binDir := filepath.Join(tmp, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	uCfg := baseCfg()
	uCfg.System.User.Rootless.Enabled = false
	uCfg.System.Username = "nonexistent_wings_test_user_999"
	Set(&uCfg)

	// useradd fails
	useraddScript := filepath.Join(binDir, "useradd")
	require.NoError(t, os.WriteFile(useraddScript, []byte("#!/bin/sh\nexit 1\n"), 0o755))
	assert.Error(t, EnsurePterodactylUser())

	// useradd succeeds (exit 0) but user still doesn't exist on lookup
	require.NoError(t, os.WriteFile(useraddScript, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	assert.Error(t, EnsurePterodactylUser())

	// 6. Docker registry helpers edge cases
	dCfg := DockerConfiguration{}
	reg, cred := dCfg.RegistryCredentialsForImage(":::invalid_image_ref")
	assert.Empty(t, reg)
	assert.Nil(t, cred)

	_, _, ok := parseDockerRegistryReference("example.com/INVALID_UPPERCASE")
	assert.False(t, ok)

	dom, pth, ok := parseDockerRegistryReference("docker.io/v2")
	assert.True(t, ok)
	assert.Equal(t, "docker.io", dom)
	assert.Equal(t, "library/v2", pth)
}
