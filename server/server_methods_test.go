package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/backup"
	"github.com/pterodactyl/wings/server/filesystem"
)

func TestServerErrorsAndPowerActions(t *testing.T) {
	// Errors
	errCrash := &crashTooFrequent{}
	assert.Equal(t, "server has crashed too soon after the last detected crash", errCrash.Error())
	assert.True(t, IsTooFrequentCrashError(errCrash))
	assert.False(t, IsTooFrequentCrashError(errors.New("other")))

	errNotExist := &serverDoesNotExist{}
	assert.Equal(t, "server does not exist on remote system", errNotExist.Error())
	assert.True(t, IsServerDoesNotExistError(errNotExist))
	assert.False(t, IsServerDoesNotExistError(errors.New("other")))

	// PowerActions
	assert.True(t, PowerAction(PowerActionStart).IsValid())
	assert.True(t, PowerAction(PowerActionStop).IsValid())
	assert.True(t, PowerAction(PowerActionRestart).IsValid())
	assert.True(t, PowerAction(PowerActionTerminate).IsValid())
	assert.False(t, PowerAction("unknown").IsValid())

	assert.True(t, PowerAction(PowerActionStart).IsStart())
	assert.True(t, PowerAction(PowerActionRestart).IsStart())
	assert.False(t, PowerAction(PowerActionStop).IsStart())
	assert.False(t, PowerAction(PowerActionTerminate).IsStart())
}

func TestServerStateAndActivities(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	// Id / ID
	assert.Equal(t, s.ID(), s.Id())

	// Protected States
	assert.False(t, s.IsInProtectedState())

	s.SetInstalling(true)
	assert.True(t, s.IsInstalling())
	assert.True(t, s.IsInProtectedState())
	s.SetInstalling(false)
	assert.False(t, s.IsInProtectedState())

	s.SetRestoring(true)
	assert.True(t, s.IsRestoring())
	assert.True(t, s.IsInProtectedState())
	s.SetRestoring(false)

	s.SetTransferring(true)
	assert.True(t, s.IsTransferring())
	assert.True(t, s.IsInProtectedState())
	s.SetTransferring(false)

	// StartTransferring
	ok := s.StartTransferring()
	assert.True(t, ok)
	assert.True(t, s.IsTransferring())
	s.SetTransferring(false)
	assert.False(t, s.IsTransferring())

	// Crash time
	assert.True(t, s.crasher.LastCrashTime().IsZero())
	now := time.Now()
	s.crasher.SetLastCrash(now)
	assert.Equal(t, now, s.crasher.LastCrashTime())

	// RequestActivity
	ra := s.NewRequestActivity("user-1", "127.0.0.1")
	ra2 := ra.SetUser("user-2")
	act := ra2.Event(models.Event("cmd"), nil)
	assert.Equal(t, "user-2", act.User.String)
}

func TestWebsocketBag(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	ws := s.Websockets()
	assert.Equal(t, 0, ws.Len())

	// Push
	u1 := uuid.New()
	var canceled1 bool
	var cancelFunc1 context.CancelFunc = func() { canceled1 = true }
	ws.Push(u1, &cancelFunc1)
	assert.Equal(t, 1, ws.Len())

	// TryPush when at max
	u2 := uuid.New()
	var canceled2 bool
	var cancelFunc2 context.CancelFunc = func() { canceled2 = true }
	userFunc2 := func() string { return "user-abc" }
	assert.False(t, ws.TryPush(u2, &cancelFunc2, userFunc2, 1))

	// TryPush when capacity exists
	assert.True(t, ws.TryPush(u2, &cancelFunc2, userFunc2, 5))
	assert.Equal(t, 2, ws.Len())

	// CancelUser
	ws.CancelUser("user-abc")
	assert.True(t, canceled2)
	assert.Equal(t, 1, ws.Len())

	// Remove
	ws.Remove(u1)
	assert.Equal(t, 0, ws.Len())
	assert.False(t, canceled1)

	// CancelAll
	u3 := uuid.New()
	var canceled3 bool
	var cancelFunc3 context.CancelFunc = func() { canceled3 = true }
	ws.Push(u3, &cancelFunc3)
	ws.CancelAll()
	assert.True(t, canceled3)
	assert.Equal(t, 0, ws.Len())
}

func TestManagerFilters(t *testing.T) {
	m := NewEmptyManager(nil)
	assert.Nil(t, m.Client())

	s1, err := New(nil)
	require.NoError(t, err)
	s1.Config().Uuid = "uuid-1"
	s1.Config().Suspended = false
	m.Add(s1)

	s2, err := New(nil)
	require.NoError(t, err)
	s2.Config().Uuid = "uuid-2"
	s2.Config().Suspended = true
	m.Add(s2)

	// Filter
	filtered := m.Filter(func(s *Server) bool {
		return s.IsSuspended()
	})
	assert.Len(t, filtered, 1)
	assert.Equal(t, "uuid-2", filtered[0].ID())

	// Find
	found := m.Find(func(s *Server) bool {
		return s.ID() == "uuid-1"
	})
	assert.NotNil(t, found)
	assert.Equal(t, "uuid-1", found.ID())

	notFound := m.Find(func(s *Server) bool {
		return s.ID() == "uuid-nonexistent"
	})
	assert.Nil(t, notFound)
}

type testEnvWithState struct {
	environment.ProcessEnvironment
	state string
}

func (e testEnvWithState) State() string {
	return e.state
}

func (e testEnvWithState) Config() *environment.Configuration {
	return &environment.Configuration{}
}

func TestServerMountsPersistStatesAndAPIResponse(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Configuration{
		AuthenticationToken: "test-token",
	}
	cfg.System.RootDirectory = tempDir
	cfg.System.Passwd.Enable = true
	cfg.System.Passwd.Directory = filepath.Join(tempDir, "passwd")
	cfg.System.MachineID.Enable = true
	cfg.System.MachineID.Directory = filepath.Join(tempDir, "machine-id")
	config.Set(cfg)

	serverDir := filepath.Join(tempDir, "server")
	require.NoError(t, os.MkdirAll(serverDir, 0755))
	fsys, err := filesystem.New(serverDir, 0, nil)
	require.NoError(t, err)

	s, err := New(nil)
	require.NoError(t, err)
	s.fs = fsys
	s.Config().Uuid = "11111111-2222-3333-4444-555555555555"
	s.Environment = testEnvWithState{state: environment.ProcessRunningState}

	// 1. Mounts
	mounts := s.Mounts()
	assert.True(t, len(mounts) >= 4)
	assert.Equal(t, "/home/container", mounts[0].Target)

	// 2. IsRunning and ToAPIResponse
	assert.True(t, s.IsRunning())
	resp := s.ToAPIResponse()
	assert.Equal(t, environment.ProcessRunningState, resp.State)
	assert.Equal(t, false, resp.IsSuspended)

	// 3. Manager PersistStates and ReadStates
	m := NewEmptyManager(nil)
	m.Add(s)
	err = m.PersistStates()
	require.NoError(t, err)

	states, err := m.ReadStates()
	require.NoError(t, err)
	assert.Equal(t, environment.ProcessRunningState, states[s.ID()])

	// 4. getServerwideIgnoredFiles
	// Without file
	ignored, err := s.getServerwideIgnoredFiles()
	assert.NoError(t, err)
	assert.Empty(t, ignored)

	// With .pteroignore
	require.NoError(t, os.WriteFile(filepath.Join(serverDir, ".pteroignore"), []byte("ignored.txt\n*.log\n"), 0644))
	ignored, err = s.getServerwideIgnoredFiles()
	assert.NoError(t, err)
	assert.Contains(t, ignored, "ignored.txt")

	// 5. ResourceUsage Reset
	ru := &ResourceUsage{}
	ru.Memory = 1024
	ru.CpuAbsolute = 50.0
	ru.Uptime = 1000
	ru.Network.TxBytes = 500
	ru.Network.RxBytes = 500
	ru.Reset()
	assert.Equal(t, uint64(0), ru.Memory)
	assert.Equal(t, float64(0), ru.CpuAbsolute)
	assert.Equal(t, int64(0), ru.Uptime)
	assert.Equal(t, uint64(0), ru.Network.TxBytes)
	assert.Equal(t, uint64(0), ru.Network.RxBytes)
}

type fullMockEnv struct {
	environment.ProcessEnvironment
	state          string
	exitCode       uint32
	oomKilled      bool
	exitErr        error
	waitForStopErr error
	started        bool
	stopped        bool
	terminated     bool
	inSitu         bool
	cfg            *environment.Configuration
	logs           []string
	bus            *events.Bus
}

func (m *fullMockEnv) Type() string                       { return "mock" }
func (m *fullMockEnv) Config() *environment.Configuration { return m.cfg }
func (m *fullMockEnv) Events() *events.Bus {
	if m.bus == nil {
		m.bus = events.NewBus()
	}
	return m.bus
}
func (m *fullMockEnv) Exists() (bool, error)                                  { return true, nil }
func (m *fullMockEnv) IsRunning(context.Context) (bool, error)                { return m.state == environment.ProcessRunningState, nil }
func (m *fullMockEnv) ExitState() (uint32, bool, error)                       { return m.exitCode, m.oomKilled, m.exitErr }
func (m *fullMockEnv) SetStopConfiguration(remote.ProcessStopConfiguration)   {}
func (m *fullMockEnv) SetImage(string)                                        {}
func (m *fullMockEnv) State() string                                          { return m.state }
func (m *fullMockEnv) SetState(s string)                                      { m.state = s }
func (m *fullMockEnv) SetLogCallback(func([]byte))                            {}
func (m *fullMockEnv) Attach(context.Context) error                           { return nil }
func (m *fullMockEnv) InSituUpdate() error                                    { m.inSitu = true; return nil }
func (m *fullMockEnv) Create() error                                          { return nil }
func (m *fullMockEnv) Destroy() error                                         { return nil }
func (m *fullMockEnv) SendCommand(string) error                               { return nil }
func (m *fullMockEnv) Readlog(int) ([]string, error)                          { return m.logs, nil }
func (m *fullMockEnv) Uptime(context.Context) (int64, error)                  { return 100, nil }
func (m *fullMockEnv) OnBeforeStart(context.Context) error                     { return nil }
func (m *fullMockEnv) Start(context.Context) error                            { m.started = true; m.state = environment.ProcessRunningState; return nil }
func (m *fullMockEnv) Stop(context.Context) error                             { m.stopped = true; m.state = environment.ProcessOfflineState; return nil }
func (m *fullMockEnv) WaitForStop(context.Context, time.Duration, bool) error {
	if m.waitForStopErr != nil {
		return m.waitForStopErr
	}
	m.stopped = true
	m.state = environment.ProcessOfflineState
	return nil
}
func (m *fullMockEnv) Terminate(context.Context, string) error                { m.terminated = true; m.state = environment.ProcessOfflineState; return nil }

type fullMockRemoteClient struct {
	remote.Client
	installStatusSuccess bool
	installStatusReinst  bool
	installStatusErr     error
	configResponse       remote.ServerConfigurationResponse
	configErr            error
	scriptResponse       remote.InstallationScript
	installScriptErr     error
	getServersFunc       func(context.Context, int) ([]remote.RawServerData, error)
	setBackupStatusErr   error
}

func (c *fullMockRemoteClient) SetInstallationStatus(_ context.Context, _ string, req remote.InstallStatusRequest) error {
	c.installStatusSuccess = req.Successful
	c.installStatusReinst = req.Reinstall
	return c.installStatusErr
}

func (c *fullMockRemoteClient) GetServerConfiguration(_ context.Context, _ string) (remote.ServerConfigurationResponse, error) {
	if c.configErr != nil {
		return remote.ServerConfigurationResponse{}, c.configErr
	}
	return c.configResponse, nil
}

func (c *fullMockRemoteClient) GetInstallationScript(_ context.Context, _ string) (remote.InstallationScript, error) {
	if c.installScriptErr != nil {
		return remote.InstallationScript{}, c.installScriptErr
	}
	return c.scriptResponse, nil
}

func (c *fullMockRemoteClient) SetBackupStatus(_ context.Context, _ string, _ remote.BackupRequest) error {
	return c.setBackupStatusErr
}

func (c *fullMockRemoteClient) GetServers(ctx context.Context, perPage int) ([]remote.RawServerData, error) {
	if c.getServersFunc != nil {
		return c.getServersFunc(ctx, perPage)
	}
	return []remote.RawServerData{
		{
			Uuid: "11111111-2222-3333-4444-555555555555",
			Settings: []byte(`{"uuid":"11111111-2222-3333-4444-555555555555"}`),
			ProcessConfiguration: []byte(`{}`),
		},
	}, nil
}

func TestServerCrashHandling(t *testing.T) {
	cfg := &config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	cfg.System.CrashDetection.Timeout = 60
	cfg.System.CrashDetection.CrashDetectionEnabled = true
	config.Set(cfg)

	remoteCli := &fullMockRemoteClient{
		configResponse: remote.ServerConfigurationResponse{
			Settings:             []byte(`{"uuid":"11111111-2222-3333-4444-555555555555","suspended":false,"crash_detection_enabled":true}`),
			ProcessConfiguration: &remote.ProcessConfiguration{},
		},
	}
	s, err := New(remoteCli)
	require.NoError(t, err)
	s.Config().Uuid = "11111111-2222-3333-4444-555555555555"

	fsys, err := filesystem.New(t.TempDir(), 0, nil)
	require.NoError(t, err)
	s.fs = fsys

	env := &fullMockEnv{
		state:    environment.ProcessRunningState,
		exitCode: 1,
		cfg:      environment.NewConfiguration(environment.Settings{}, []string{}),
	}
	s.Environment = env
	s.Config().CrashDetectionEnabled = true

	// 1. Server is running (not offline) -> returns nil
	err = s.handleServerCrash()
	assert.NoError(t, err)

	// 2. Server offline but CrashDetectionEnabled is false -> returns nil
	env.state = environment.ProcessOfflineState
	s.Config().CrashDetectionEnabled = false
	err = s.handleServerCrash()
	assert.NoError(t, err)

	// 3. Exit code 0 and not OOM and DetectCleanExitAsCrash false -> returns nil
	s.Config().CrashDetectionEnabled = true
	env.exitCode = 0
	env.oomKilled = false
	err = s.handleServerCrash()
	assert.NoError(t, err)

	// 4. Server crashed with exit code 1 -> triggers restart
	env.exitCode = 1
	err = s.handleServerCrash()
	assert.NoError(t, err)
	assert.True(t, env.started)
	assert.False(t, s.crasher.LastCrashTime().IsZero())

	// 5. Frequent crash within timeout -> returns crashTooFrequent
	env.state = environment.ProcessOfflineState
	err = s.handleServerCrash()
	assert.Error(t, err)
	assert.True(t, IsTooFrequentCrashError(err))
}

func TestServerSyncAndInstall(t *testing.T) {
	config.Set(&config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	})
	serverUUID := "99999999-8888-7777-6666-555555555555"
	remoteCli := &fullMockRemoteClient{
		configResponse: remote.ServerConfigurationResponse{
			Settings:             []byte(`{"uuid":"` + serverUUID + `","suspended":false,"skip_egg_scripts":true}`),
			ProcessConfiguration: &remote.ProcessConfiguration{},
		},
	}
	s, err := New(remoteCli)
	require.NoError(t, err)
	s.Config().Uuid = serverUUID

	fsys, err := filesystem.New(t.TempDir(), 0, nil)
	require.NoError(t, err)
	s.fs = fsys

	envCfg := environment.NewConfiguration(environment.Settings{}, []string{})
	env := &fullMockEnv{
		state: environment.ProcessOfflineState,
		cfg:   envCfg,
		logs:  []string{"line1", "line2"},
	}
	s.Environment = env

	// Sync
	err = s.Sync()
	require.NoError(t, err)
	assert.Equal(t, serverUUID, s.ID())

	// SyncWithEnvironment
	s.SyncWithEnvironment()
	assert.True(t, env.inSitu)

	// SyncWithEnvironment when suspended
	s.Config().SetSuspended(true)
	env.state = environment.ProcessRunningState
	s.SyncWithEnvironment()
	// Allow background goroutine to call WaitForStop
	time.Sleep(50 * time.Millisecond)
	assert.True(t, env.stopped)

	// Install with SkipEggScripts = true
	s.Config().SetSuspended(false)
	s.Config().SkipEggScripts = true
	err = s.Install()
	assert.NoError(t, err)
	assert.True(t, remoteCli.installStatusSuccess)
	assert.False(t, remoteCli.installStatusReinst)

	// Reinstall with SkipEggScripts = true
	err = s.Reinstall()
	assert.NoError(t, err)
	assert.True(t, remoteCli.installStatusSuccess)
	assert.True(t, remoteCli.installStatusReinst)

	// Install with SkipEggScripts = false, where script fetch fails
	s.Config().SkipEggScripts = false
	remoteCli.installScriptErr = errors.New("script fetch error")
	remoteCli.installStatusErr = errors.New("status notify error")
	err = s.Install()
	assert.Error(t, err)

	// Reinstall when running: WaitForStop fails
	env.state = environment.ProcessRunningState
	env.waitForStopErr = errors.New("failed to stop")
	err = s.Reinstall()
	assert.Error(t, err)
	env.waitForStopErr = nil

	// Reinstall when running: WaitForStop succeeds then Sync fails
	env.state = environment.ProcessRunningState
	remoteCli.configErr = errors.New("sync failed")
	err = s.Reinstall()
	assert.Error(t, err)
	remoteCli.configErr = nil
	remoteCli.installScriptErr = nil
	remoteCli.installStatusErr = nil

	// ReadLogfile
	lines, err := s.ReadLogfile(100)
	require.NoError(t, err)
	assert.Equal(t, []string{"line1", "line2"}, lines)
}

func TestServerPowerAndOnBeforeStart(t *testing.T) {
	config.Set(&config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	})
	serverDir := filepath.Join(t.TempDir(), "server_power")
	require.NoError(t, os.MkdirAll(serverDir, 0755))

	fsys, err := filesystem.New(serverDir, 0, nil)
	require.NoError(t, err)

	remoteCli := &fullMockRemoteClient{
		configResponse: remote.ServerConfigurationResponse{
			Settings:             []byte(`{"uuid":"33333333-4444-5555-6666-777777777777","suspended":false}`),
			ProcessConfiguration: &remote.ProcessConfiguration{},
		},
	}
	s, err := New(remoteCli)
	require.NoError(t, err)
	s.fs = fsys
	s.Config().Uuid = "33333333-4444-5555-6666-777777777777"

	envCfg := environment.NewConfiguration(environment.Settings{}, []string{})
	env := &fullMockEnv{
		state: environment.ProcessOfflineState,
		cfg:   envCfg,
	}
	s.Environment = env

	// Protected state checks
	s.SetInstalling(true)
	assert.Equal(t, ErrServerIsInstalling, s.HandlePowerAction(PowerActionStart))
	s.SetInstalling(false)

	s.SetTransferring(true)
	assert.Equal(t, ErrServerIsTransferring, s.HandlePowerAction(PowerActionStart))
	s.SetTransferring(false)

	s.SetRestoring(true)
	assert.Equal(t, ErrServerIsRestoring, s.HandlePowerAction(PowerActionStart))
	s.SetRestoring(false)

	// Suspended server
	s.Config().SetSuspended(true)
	remoteCli.configResponse.Settings = []byte(`{"uuid":"33333333-4444-5555-6666-777777777777","suspended":true}`)
	assert.Equal(t, ErrSuspended, s.HandlePowerAction(PowerActionStart))
	s.Config().SetSuspended(false)
	remoteCli.configResponse.Settings = []byte(`{"uuid":"33333333-4444-5555-6666-777777777777","suspended":false}`)

	// CheckPermissionsOnBoot and disk space check
	cfgObj := config.Get()
	cfgObj.System.CheckPermissionsOnBoot = true
	cfgObj.System.User.Uid = os.Getuid()
	cfgObj.System.User.Gid = os.Getgid()
	config.Set(cfgObj)
	s.Config().Build.DiskSpace = 1024

	// Start power action
	err = s.HandlePowerAction(PowerActionStart)
	assert.NoError(t, err)
	assert.True(t, env.started)
	assert.Equal(t, environment.ProcessRunningState, env.state)

	// Starting while running returns ErrIsRunning
	assert.Equal(t, ErrIsRunning, s.HandlePowerAction(PowerActionStart))

	// Stop power action
	err = s.HandlePowerAction(PowerActionStop)
	assert.NoError(t, err)
	assert.True(t, env.stopped)

	// Restart power action
	err = s.HandlePowerAction(PowerActionRestart)
	assert.NoError(t, err)

	// Terminate power action
	err = s.HandlePowerAction(PowerActionTerminate)
	assert.NoError(t, err)
	assert.True(t, env.terminated)

	// CreateEnvironment with machine ID
	cfg := config.Get()
	cfg.System.MachineID.Enable = true
	cfg.System.MachineID.Directory = t.TempDir()
	config.Set(cfg)

	err = s.CreateEnvironment()
	assert.NoError(t, err)
	machineIdPath := filepath.Join(cfg.System.MachineID.Directory, s.ID())
	assert.FileExists(t, machineIdPath)
}

func TestServerActivityAndDatabase(t *testing.T) {
	cfg := &config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	cfg.System.RootDirectory = t.TempDir()
	config.Set(cfg)

	func() {
		defer func() { _ = recover() }()
		_ = database.Initialize()
	}()

	s, err := New(nil)
	require.NoError(t, err)

	reqAct := s.NewRequestActivity("usr-1", "127.0.0.1")
	assert.Equal(t, s.ID(), reqAct.server)
	assert.Equal(t, "usr-1", reqAct.user)
	assert.Equal(t, "127.0.0.1", reqAct.ip)

	reqAct2 := reqAct.SetUser("usr-2")
	assert.Equal(t, "usr-2", reqAct2.user)

	ev := reqAct2.Event(ActivityConsoleCommand, models.ActivityMeta{"cmd": "say hello"})
	assert.Equal(t, ActivityConsoleCommand, ev.Event)
	assert.Equal(t, "usr-2", ev.User.String)

	if database.Instance() != nil {
		s.SaveActivity(reqAct2, ActivityConsoleCommand, models.ActivityMeta{"cmd": "say hello"})
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServerBackupMethods(t *testing.T) {
	tempDir := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test",
		System: config.SystemConfiguration{
			RootDirectory:   tempDir,
			BackupDirectory: tempDir,
		},
	})

	remoteCli := &fullMockRemoteClient{}
	s, err := New(remoteCli)
	require.NoError(t, err)

	fs, err := filesystem.New(tempDir, 1024*1024*1024, nil)
	require.NoError(t, err)
	s.fs = fs

	// Test getServerwideIgnoredFiles when no .pteroignore exists
	ign, err := s.getServerwideIgnoredFiles()
	assert.NoError(t, err)
	assert.Empty(t, ign)

	// Write valid .pteroignore
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, ".pteroignore"), []byte("temp/\n*.log\n"), 0644))
	ign, err = s.getServerwideIgnoredFiles()
	assert.NoError(t, err)
	assert.Equal(t, "temp/\n*.log\n", ign)

	// notifyPanelOfBackup
	ad := &backup.ArchiveDetails{
		Checksum:     "abc123",
		ChecksumType: "sha1",
		Size:         1024,
	}
	assert.NoError(t, s.notifyPanelOfBackup("backup-uuid-1", ad, true))
	assert.NoError(t, s.notifyPanelOfBackup("backup-uuid-1", ad, false))

	// Backup with local driver
	b := backup.NewLocal(remoteCli, uuid.New().String(), "")
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "file.txt"), []byte("sample file data"), 0644))
	err = s.Backup(b)
	assert.NoError(t, err)

	// getServerwideIgnoredFiles tests
	_ = os.Remove(filepath.Join(tempDir, ".pteroignore"))
	ign, err = s.getServerwideIgnoredFiles()
	assert.NoError(t, err)
	assert.Empty(t, ign)

	// Valid .pteroignore
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, ".pteroignore"), []byte("*.log\n"), 0644))
	ign, err = s.getServerwideIgnoredFiles()
	assert.NoError(t, err)
	assert.Equal(t, "*.log\n", ign)

	// Invalid .pteroignore
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, ".pteroignore"), []byte(strings.Repeat("*a", 17)), 0644))
	_, err = s.getServerwideIgnoredFiles()
	assert.Error(t, err)

	// Backup with invalid .pteroignore
	b2 := backup.NewLocal(remoteCli, uuid.New().String(), "")
	assert.NoError(t, s.Backup(b2))

	// Too large .pteroignore
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, ".pteroignore"), make([]byte, filesystem.MaxIgnoreLength+10), 0644))
	ign, err = s.getServerwideIgnoredFiles()
	assert.NoError(t, err)
	assert.Empty(t, ign)
	_ = os.Remove(filepath.Join(tempDir, ".pteroignore"))

	// notifyPanelOfBackup with remote.RequestError
	remoteCli.setBackupStatusErr = &remote.RequestError{Detail: "bad request"}
	assert.Error(t, s.notifyPanelOfBackup("backup-uuid-1", ad, true))

	// Backup notifyPanelOfBackup returns error (b.Remove called, returns generate err)
	remoteCli.setBackupStatusErr = errors.New("cannot notify")
	b3 := backup.NewLocal(remoteCli, uuid.New().String(), "")
	assert.NoError(t, s.Backup(b3))
	remoteCli.setBackupStatusErr = nil
}

func TestServerStateTransitionsAndListeners(t *testing.T) {
	remoteCli := &fullMockRemoteClient{}
	s, err := New(remoteCli)
	require.NoError(t, err)
	s.procConfig = &remote.ProcessConfiguration{}

	tempDir := filepath.Join(t.TempDir(), "server_data")
	fs, err := filesystem.New(tempDir, 1024*1024*1024, nil)
	require.NoError(t, err)
	s.fs = fs

	// EnsureDataDirectoryExists creates dir
	assert.NoError(t, s.EnsureDataDirectoryExists())
	assert.DirExists(t, tempDir)
	// EnsureDataDirectoryExists when already exists
	assert.NoError(t, s.EnsureDataDirectoryExists())

	env := &fullMockEnv{
		bus:   events.NewBus(),
		state: environment.ProcessStartingState,
	}
	s.Environment = env

	assert.True(t, s.IsRunning())

	// StartEventListeners
	s.StartEventListeners()

	// Process console output event
	s.processConsoleOutputEvent([]byte("log line output"))

	// Trigger environment events
	s.Environment.Events().Publish(environment.ResourceEvent, []byte(`{"memory_bytes":1048576,"memory_limit_bytes":10485760}`))
	s.Environment.Events().Publish(environment.StateChangeEvent, []byte(`"running"`))
	time.Sleep(10 * time.Millisecond)

	// OnStateChange to offline
	env.state = environment.ProcessOfflineState
	s.OnStateChange()
	assert.False(t, s.IsRunning())
}

func TestManagerAndInstallAndSync(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	cfg.System.Data = tempDir
	cfg.System.TmpDirectory = filepath.Join(tempDir, "tmp")
	cfg.System.RootDirectory = tempDir
	cfg.System.LogDirectory = filepath.Join(tempDir, "logs")
	cfg.Docker.InstallerLimits.Memory = 512
	cfg.Docker.InstallerLimits.Cpu = 100
	config.Set(cfg)

	remoteCli := &fullMockRemoteClient{
		configResponse: remote.ServerConfigurationResponse{
			Settings:             []byte(`{"uuid":"11111111-2222-3333-4444-555555555555","suspended":false}`),
			ProcessConfiguration: &remote.ProcessConfiguration{},
		},
	}

	// 1. NewManager
	mgr, err := NewManager(context.Background(), remoteCli)
	require.NoError(t, err)
	assert.Equal(t, 1, mgr.Len())
	assert.Equal(t, remoteCli, mgr.Client())

	s, ok := mgr.Get("11111111-2222-3333-4444-555555555555")
	require.True(t, ok)

	// Mock environment on server
	envCfg := environment.NewConfiguration(environment.Settings{
		Limits: environment.Limits{MemoryLimit: 256, CpuLimit: 50},
	}, []string{})
	env := &fullMockEnv{
		cfg:   envCfg,
		bus:   events.NewBus(),
		state: environment.ProcessRunningState,
	}
	s.Environment = env

	// 2. PersistStates and ReadStates
	assert.NoError(t, mgr.PersistStates())
	states, err := mgr.ReadStates()
	require.NoError(t, err)
	assert.Equal(t, "running", states[s.ID()])

	// 3. StartTransferring
	assert.True(t, s.StartTransferring())
	assert.True(t, s.IsTransferring())
	assert.False(t, s.StartTransferring()) // already transferring

	s.SetTransferring(false)
	assert.False(t, s.IsTransferring())

	s.SetInstalling(true)
	assert.False(t, s.StartTransferring())
	s.SetInstalling(false)

	s.SetRestoring(true)
	assert.False(t, s.StartTransferring())
	s.SetRestoring(false)

	// 4. InstallationProcess writeScriptToDisk and resourceLimits
	ip := &InstallationProcess{
		Server: s,
		Script: &remote.InstallationScript{
			ContainerImage: "alpine:latest",
			Entrypoint:     "/bin/sh",
			Script:         "echo hello world\r\nexit 0\r\n",
		},
	}
	assert.NoError(t, ip.writeScriptToDisk())
	scriptBytes, err := os.ReadFile(filepath.Join(ip.tempDir(), "install.sh"))
	require.NoError(t, err)
	assert.Equal(t, "echo hello world\nexit 0\n", string(scriptBytes))

	limits := ip.resourceLimits()
	assert.True(t, limits.Memory > 0)
	assert.NotNil(t, limits.PidsLimit)

	instProc, err := NewInstallationProcess(s, ip.Script)
	if err == nil && instProc != nil {
		_ = instProc.RemoveContainer()
	}

	// 5. SyncWithEnvironment
	s.Config().Suspended = false
	s.SyncWithEnvironment()
	assert.True(t, env.inSitu)

	s.Config().Suspended = true
	s.SyncWithEnvironment()
	time.Sleep(50 * time.Millisecond)
	assert.True(t, env.stopped)
}

func TestServerEnsureDataDirectoryExists(t *testing.T) {
	cfg := &config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	cfg.System.User.Uid = os.Getuid()
	cfg.System.User.Gid = os.Getgid()
	config.Set(cfg)

	tmpDir := t.TempDir()
	serverDir := filepath.Join(tmpDir, "server_data_dir")
	fsys, err := filesystem.New(serverDir, 0, nil)
	require.NoError(t, err)

	s, err := New(nil)
	require.NoError(t, err)
	s.fs = fsys

	// 1. Directory doesn't exist yet
	_ = os.RemoveAll(serverDir)
	assert.NoError(t, s.EnsureDataDirectoryExists())
	assert.DirExists(t, serverDir)

	// 2. Directory already exists
	assert.NoError(t, s.EnsureDataDirectoryExists())

	// 3. Stat error (path has a file where directory expected)
	parentDir := t.TempDir()
	childDir := filepath.Join(parentDir, "sub", "server_data")
	badFsys, err := filesystem.New(childDir, 0, nil)
	require.NoError(t, err)
	s.fs = badFsys
	require.NoError(t, os.RemoveAll(filepath.Join(parentDir, "sub")))
	require.NoError(t, os.WriteFile(filepath.Join(parentDir, "sub"), []byte("not a dir"), 0644))
	assert.Error(t, s.EnsureDataDirectoryExists())
}

func TestInstallationProcessDetails(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	cfg.System.TmpDirectory = tmpDir
	cfg.Docker.InstallerLimits.Memory = 512
	cfg.Docker.InstallerLimits.Cpu = 100
	config.Set(cfg)

	s, err := New(nil)
	require.NoError(t, err)
	s.Config().Uuid = "test-uuid-install"
	s.Config().Build.MemoryLimit = 256
	s.Config().Build.CpuLimit = 50

	ip := &InstallationProcess{
		Server: s,
		Script: &remote.InstallationScript{
			Script: "echo hello\r\necho world\r\n",
		},
	}

	// 1. tempDir
	assert.Equal(t, filepath.Join(tmpDir, "test-uuid-install"), ip.tempDir())

	// 2. writeScriptToDisk
	require.NoError(t, ip.writeScriptToDisk())
	scriptContent, err := os.ReadFile(filepath.Join(ip.tempDir(), "install.sh"))
	require.NoError(t, err)
	assert.Equal(t, "echo hello\necho world\n", string(scriptContent))

	// 3. resourceLimits with memory and cpu boosted
	res := ip.resourceLimits()
	assert.True(t, res.Memory > 0)
	assert.True(t, res.CPUQuota > 0)

	// 4. resourceLimits with Cpu == 0
	cfg.Docker.InstallerLimits.Cpu = 0
	config.Set(cfg)
	resZero := ip.resourceLimits()
	assert.Equal(t, int64(0), resZero.CPUQuota)

	// 5. NewInstallationProcess and methods
	cfg.System.LogDirectory = tmpDir
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "install"), 0755))

	proc, err := NewInstallationProcess(s, &remote.InstallationScript{
		ContainerImage: "ghcr.io/invalid/nonexistent-image:invalid",
		Entrypoint:     "/bin/sh",
		Script:         "echo 1",
	})
	if err == nil {
		assert.Equal(t, filepath.Join(tmpDir, "install", s.ID()+".log"), proc.GetLogPath())
		assert.NoError(t, proc.RemoveContainer())
		assert.Error(t, proc.BeforeExecute())
	}
}

func TestManagerInitBranches(t *testing.T) {
	config.Set(&config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	})
	cfg := config.Get()
	cfg.System.Data = t.TempDir()
	config.Set(cfg)

	validUUID := "11111111-2222-3333-4444-555555555555"
	badCfgUUID := "22222222-3333-4444-5555-666666666666"
	badSetUUID := "33333333-4444-5555-6666-777777777777"

	remoteCli := &fullMockRemoteClient{
		getServersFunc: func(ctx context.Context, perPage int) ([]remote.RawServerData, error) {
			return []remote.RawServerData{
				// 1. Valid server
				{
					Uuid:                 validUUID,
					Settings:             []byte(`{"uuid":"` + validUUID + `"}`),
					ProcessConfiguration: []byte(`{}`),
				},
				// 2. Server with bad process config JSON
				{
					Uuid:                 badCfgUUID,
					Settings:             []byte(`{"uuid":"` + badCfgUUID + `"}`),
					ProcessConfiguration: []byte(`invalid-json`),
				},
				// 3. Server with bad settings JSON (causes InitServer to fail)
				{
					Uuid:                 badSetUUID,
					Settings:             []byte(`invalid-settings`),
					ProcessConfiguration: []byte(`{}`),
				},
				// 4. Duplicate valid server (causes !AddIfMissing)
				{
					Uuid:                 validUUID,
					Settings:             []byte(`{"uuid":"` + validUUID + `"}`),
					ProcessConfiguration: []byte(`{}`),
				},
			}, nil
		},
	}

	mgr, err := NewManager(context.Background(), remoteCli)
	require.NoError(t, err)
	assert.Equal(t, 1, mgr.Len())

	// Client.GetServers error
	errRemoteCli := &fullMockRemoteClient{
		getServersFunc: func(ctx context.Context, perPage int) ([]remote.RawServerData, error) {
			return nil, errors.New("cannot fetch servers")
		},
	}
	_, err = NewManager(context.Background(), errRemoteCli)
	assert.Error(t, err)
}

func TestDiskSpaceLimiter(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)
	env := &fullMockEnv{
		waitForStopErr: errors.New("stop error"),
	}
	s.Environment = env
	dsl := newDiskLimiter(s)
	dsl.Trigger()
	// Triggering second time is a no-op due to sync.Once
	dsl.Trigger()
	// Reset allows trigger again
	dsl.Reset()
	dsl.Trigger()
}






