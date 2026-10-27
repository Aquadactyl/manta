package server

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/buger/jsonparser"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/parser"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/backup"
	"github.com/pterodactyl/wings/server/filesystem"
)

func init() {
	config.Set(&config.Configuration{
		AuthenticationToken: "abc",
		System: config.SystemConfiguration{
			RootDirectory:     "/tmp",
			Data:              "/tmp/volumes",
			ArchiveDirectory:  "/tmp/archives",
			BackupDirectory:   "/tmp/backups",
			LogDirectory:      "/tmp/logs",
			TmpDirectory:      "/tmp",
			DiskCheckInterval: 150,
			Backups: config.Backups{
				WriteLimit:       0,
				CompressionLevel: "gzip",
			},
		},
	})
}

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
	mu             sync.RWMutex
	state          string
	exitCode       uint32
	oomKilled      bool
	exitErr        error
	waitForStopErr error
	started        bool
	stopped        bool
	terminated     bool
	inSitu         bool
	inSituErr      error
	cfg            *environment.Configuration
	logs           []string
	bus            *events.Bus
}

func (m *fullMockEnv) setExitState(code uint32, oom bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exitCode = code
	m.oomKilled = oom
	m.exitErr = err
}

func (m *fullMockEnv) setInSituErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inSituErr = err
}

func (m *fullMockEnv) setWaitForStopErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.waitForStopErr = err
}

func (m *fullMockEnv) Stopped() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stopped
}

func (m *fullMockEnv) InSitu() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inSitu
}

func (m *fullMockEnv) Started() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.started
}

func (m *fullMockEnv) Terminated() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.terminated
}

func (m *fullMockEnv) Type() string                       { return "mock" }
func (m *fullMockEnv) Config() *environment.Configuration { return m.cfg }
func (m *fullMockEnv) Events() *events.Bus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bus == nil {
		m.bus = events.NewBus()
	}
	return m.bus
}
func (m *fullMockEnv) Exists() (bool, error) { return true, nil }
func (m *fullMockEnv) IsRunning(context.Context) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state == environment.ProcessRunningState, nil
}
func (m *fullMockEnv) ExitState() (uint32, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.exitCode, m.oomKilled, m.exitErr
}
func (m *fullMockEnv) SetStopConfiguration(remote.ProcessStopConfiguration) {}
func (m *fullMockEnv) SetImage(string)                                      {}
func (m *fullMockEnv) State() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}
func (m *fullMockEnv) SetState(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = s
}
func (m *fullMockEnv) SetLogCallback(func([]byte))  {}
func (m *fullMockEnv) Attach(context.Context) error { return nil }
func (m *fullMockEnv) InSituUpdate() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inSitu = true
	return m.inSituErr
}
func (m *fullMockEnv) Create() error                         { return nil }
func (m *fullMockEnv) Destroy() error                        { return nil }
func (m *fullMockEnv) SendCommand(string) error              { return nil }
func (m *fullMockEnv) Readlog(int) ([]string, error)         { return m.logs, nil }
func (m *fullMockEnv) Uptime(context.Context) (int64, error) { return 100, nil }
func (m *fullMockEnv) OnBeforeStart(context.Context) error   { return nil }
func (m *fullMockEnv) Start(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	m.state = environment.ProcessRunningState
	return nil
}
func (m *fullMockEnv) Stop(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	m.state = environment.ProcessOfflineState
	return nil
}
func (m *fullMockEnv) WaitForStop(context.Context, time.Duration, bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.waitForStopErr != nil {
		return m.waitForStopErr
	}
	m.stopped = true
	m.state = environment.ProcessOfflineState
	return nil
}
func (m *fullMockEnv) Terminate(context.Context, string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terminated = true
	m.state = environment.ProcessOfflineState
	return nil
}

type fullMockRemoteClient struct {
	remote.Client
	installStatusSuccess     bool
	installStatusReinst      bool
	installStatusErr         error
	configResponse           remote.ServerConfigurationResponse
	configErr                error
	scriptResponse           remote.InstallationScript
	installScriptErr         error
	getServersFunc           func(context.Context, int) ([]remote.RawServerData, error)
	setBackupStatusErr       error
	sendRestorationStatusErr error
}

func (c *fullMockRemoteClient) SendRestorationStatus(_ context.Context, _ string, _ bool) error {
	return c.sendRestorationStatusErr
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
			Uuid:                 "11111111-2222-3333-4444-555555555555",
			Settings:             []byte(`{"uuid":"11111111-2222-3333-4444-555555555555"}`),
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
	assert.True(t, env.InSitu())

	// SyncWithEnvironment when suspended
	s.Config().SetSuspended(true)
	env.SetState(environment.ProcessRunningState)
	s.SyncWithEnvironment()
	// Allow background goroutine to call WaitForStop
	time.Sleep(50 * time.Millisecond)
	assert.True(t, env.Stopped())

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
	time.Sleep(50 * time.Millisecond)

	// OnStateChange to offline
	env.SetState(environment.ProcessOfflineState)
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
	assert.True(t, env.InSitu())

	s.Config().Suspended = true
	s.SyncWithEnvironment()
	time.Sleep(50 * time.Millisecond)
	assert.True(t, env.Stopped())
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

type mockFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
	sys     any
}

func (m *mockFileInfo) Name() string       { return m.name }
func (m *mockFileInfo) Size() int64        { return m.size }
func (m *mockFileInfo) Mode() os.FileMode  { return m.mode }
func (m *mockFileInfo) ModTime() time.Time { return m.modTime }
func (m *mockFileInfo) IsDir() bool        { return m.isDir }
func (m *mockFileInfo) Sys() any           { return m.sys }

func TestServerRemainingCoverage(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	}
	cfg.System.Data = tempDir
	cfg.System.TmpDirectory = filepath.Join(tempDir, "tmp")
	cfg.System.RootDirectory = tempDir
	cfg.System.LogDirectory = filepath.Join(tempDir, "logs")
	cfg.System.DiskCheckInterval = 150
	config.Set(cfg)

	func() {
		defer func() { _ = recover() }()
		_ = database.Initialize()
	}()

	remoteCli := &fullMockRemoteClient{
		configResponse: remote.ServerConfigurationResponse{
			Settings:             []byte(`{"uuid":"11111111-2222-3333-4444-555555555555","suspended":false}`),
			ProcessConfiguration: &remote.ProcessConfiguration{},
		},
	}
	s, err := New(remoteCli)
	require.NoError(t, err)

	envCfg := environment.NewConfiguration(environment.Settings{
		Limits: environment.Limits{MemoryLimit: 256, CpuLimit: 50},
	}, []string{})
	env := &fullMockEnv{
		cfg:   envCfg,
		bus:   events.NewBus(),
		state: environment.ProcessStartingState,
	}
	s.Environment = env
	s.procConfig = &remote.ProcessConfiguration{}
	s.cfg.Uuid = "11111111-2222-3333-4444-555555555555"

	fs, err := filesystem.New(filepath.Join(tempDir, s.ID()), 1024*1024*1024, nil)
	require.NoError(t, err)
	s.fs = fs

	// 1. Activity SaveActivity with canceled context -> triggers tx.Error != nil
	reqAct := s.NewRequestActivity("usr-1", "127.0.0.1")
	s.CtxCancel()
	s.SaveActivity(reqAct, ActivityConsoleCommand, models.ActivityMeta{"cmd": "say hello"})
	time.Sleep(10 * time.Millisecond)

	// Recreate server context for subsequent tests
	newCtx, newCancel := context.WithCancel(context.Background())
	s.ctx = newCtx
	s.ctxCancel = &newCancel

	// 2. Crash handling with exitErr != nil
	env.exitErr = errors.New("cannot get exit state")
	env.state = environment.ProcessOfflineState
	s.Config().CrashDetectionEnabled = true
	assert.Error(t, s.handleServerCrash())
	env.exitErr = nil

	// 3. WebsocketBag nil conns map in TryPush
	bag := &WebsocketBag{}
	assert.True(t, bag.TryPush(uuid.New(), nil, func() string { return "user" }, 10))

	// 4. Listeners DockerImagePullStarted and DockerImagePullCompleted
	s.StartEventListeners()
	s.Environment.Events().Publish(environment.DockerImagePullStarted, []byte{})
	s.Environment.Events().Publish(environment.DockerImagePullCompleted, []byte{})
	time.Sleep(10 * time.Millisecond)

	// 5. Update SyncWithEnvironment InSituUpdate error
	env.setInSituErr(errors.New("cannot update in situ"))
	s.Config().SetSuspended(false)
	s.SyncWithEnvironment()
	env.setInSituErr(nil)

	// Suspended with running process & WaitForStop error
	s.Config().SetSuspended(true)
	env.SetState(environment.ProcessRunningState)
	env.setWaitForStopErr(errors.New("cannot stop"))
	s.SyncWithEnvironment()
	time.Sleep(50 * time.Millisecond)
	env.setWaitForStopErr(nil)
	s.Config().SetSuspended(false)

	// 6. Config parser panic recovery and file touch error
	s.procConfig.ConfigurationFiles = []parser.ConfigurationFile{
		{
			FileName: "invalid\x00path",
			Parser:   parser.File,
		},
		{
			FileName: "panic.file",
			Parser:   "unknown_parser_triggers_panic",
		},
	}
	s.UpdateConfigurationFiles()

	// 7. Mounts resolveMountPath relative path & symlink loop
	_, err = resolveMountPath("relative/path", 1000)
	assert.Error(t, err)

	loopPath := filepath.Join(tempDir, "loop_mount")
	_ = os.Symlink(loopPath, loopPath)
	_, err = resolveMountPath(loopPath, 1000)
	assert.Error(t, err)

	// 8. Manager ReadStates with corrupted JSON
	statesFile := filepath.Join(tempDir, "states.json")
	require.NoError(t, os.WriteFile(statesFile, []byte("{broken_json"), 0644))
	mgr := NewEmptyManager(remoteCli)
	_, err = mgr.ReadStates()
	assert.Error(t, err)

	// Init with remote.RequestError
	reqErrClient := &fullMockRemoteClient{
		getServersFunc: func(ctx context.Context, perPage int) ([]remote.RawServerData, error) {
			return nil, &remote.RequestError{Detail: "request failed"}
		},
	}
	_, err = NewManager(context.Background(), reqErrClient)
	assert.Error(t, err)

	// 9. Backup RestoreBackup with reader & truncate
	oldFs := s.fs
	fakeReader := io.NopCloser(strings.NewReader("fake backup data"))
	mockBackup := backup.NewLocal(remoteCli, uuid.New().String(), "")

	// RestoreBackup with WaitForStop error
	env.SetState(environment.ProcessRunningState)
	env.setWaitForStopErr(errors.New("cannot stop"))
	assert.Error(t, s.RestoreBackup(mockBackup, fakeReader, false))
	time.Sleep(50 * time.Millisecond)
	env.setWaitForStopErr(nil)
	env.SetState(environment.ProcessOfflineState)

	_ = s.RestoreBackup(mockBackup, fakeReader, false)

	remoteCli.sendRestorationStatusErr = errors.New("cannot send status")
	_ = s.RestoreBackup(mockBackup, nil, true)
	remoteCli.sendRestorationStatusErr = nil

	// restoreEntry branches
	assert.NoError(t, s.restoreEntry("test_dir", &mockFileInfo{isDir: true, mode: os.ModeDir | 0755}, nil))
	assert.NoError(t, s.restoreEntry("test_pipe", &mockFileInfo{mode: os.ModeNamedPipe}, nil))
	assert.NoError(t, s.restoreEntry("test_symlink", &mockFileInfo{
		mode: os.ModeSymlink,
		sys:  &tar.Header{Linkname: "target.txt"},
	}, nil))

	// restoreEntry: directory error
	blockDir := filepath.Join(s.Filesystem().Path(), "block_dir_file")
	require.NoError(t, os.WriteFile(blockDir, []byte("x"), 0644))
	hdrLinkBlocked := &tar.Header{Typeflag: tar.TypeSymlink, Linkname: "target.txt"}
	fiLinkBlocked := &mockFileInfo{name: "block_dir_file/child/sym", isDir: false, mode: os.ModeSymlink, sys: hdrLinkBlocked}
	assert.Error(t, s.restoreEntry("block_dir_file/child/sym", fiLinkBlocked, strings.NewReader("")))

	// restoreEntry: symlink creation error
	hdrLinkNull := &tar.Header{Typeflag: tar.TypeSymlink, Linkname: "target.txt"}
	fiLinkNull := &mockFileInfo{name: "sym_dir/null\x00sym", isDir: false, mode: os.ModeSymlink, sys: hdrLinkNull}
	assert.Error(t, s.restoreEntry("sym_dir/null\x00sym", fiLinkNull, strings.NewReader("")))

	// TruncateRootDirectory error in RestoreBackup
	truncateFsDir := filepath.Join(tempDir, "truncate_fail")
	truncateFs, err := filesystem.New(truncateFsDir, 0, nil)
	require.NoError(t, err)
	immFile := filepath.Join(truncateFsDir, "imm.txt")
	require.NoError(t, os.WriteFile(immFile, []byte("x"), 0644))
	if exec.Command("chattr", "+i", immFile).Run() == nil {
		s.fs = truncateFs
		assert.Error(t, s.RestoreBackup(mockBackup, nil, true))
		_ = exec.Command("chattr", "-i", immFile).Run()
		s.fs = oldFs
	}

	// 10. Power HandlePowerAction timeout, terminate without lock, restart onBeforeStart error, unknown action
	_ = s.powerLock.Acquire()
	err = s.HandlePowerAction(PowerActionStart, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "could not acquire lock")
	s.powerLock.Release()

	_ = s.powerLock.Acquire()
	assert.NoError(t, s.HandlePowerAction(PowerActionTerminate))
	s.powerLock.Release()

	remoteCli.configErr = errors.New("cannot sync")
	env.state = environment.ProcessRunningState
	assert.Error(t, s.HandlePowerAction(PowerActionRestart))
	remoteCli.configErr = nil

	// PowerActionRestart with WaitForStop error
	env.waitForStopErr = errors.New("wait for stop failed")
	assert.Error(t, s.HandlePowerAction(PowerActionRestart))
	env.waitForStopErr = nil

	assert.Error(t, s.HandlePowerAction("invalid_action"))

	// onBeforeStart disk space exceeded
	remoteCli.configResponse.Settings = []byte(`{"uuid":"11111111-2222-3333-4444-555555555555","suspended":false,"build":{"disk_space":1}}`)
	s.Filesystem().SetDiskLimit(1)
	require.NoError(t, os.WriteFile(filepath.Join(s.Filesystem().Path(), "oversize.dat"), make([]byte, 2*1024*1024), 0644))
	assert.Error(t, s.onBeforeStart())
	remoteCli.configResponse.Settings = []byte(`{"uuid":"11111111-2222-3333-4444-555555555555","suspended":false}`)
	s.Filesystem().SetDiskLimit(0)

	// onBeforeStart CheckPermissionsOnBoot
	cfg.System.CheckPermissionsOnBoot = true
	config.Set(cfg)
	assert.NoError(t, s.onBeforeStart())
	cfg.System.CheckPermissionsOnBoot = false
	config.Set(cfg)

	// onBeforeStart sync error
	remoteCli.configErr = errors.New("sync failed")
	assert.Error(t, s.onBeforeStart())
	remoteCli.configErr = nil

	// onBeforeStart suspended check
	remoteCli.configResponse.Settings = []byte(`{"uuid":"11111111-2222-3333-4444-555555555555","suspended":true}`)
	assert.Equal(t, ErrSuspended, s.onBeforeStart())
	remoteCli.configResponse.Settings = []byte(`{"uuid":"11111111-2222-3333-4444-555555555555","suspended":false}`)
	_ = s.Sync()

	// SyncWithConfiguration error
	remoteCli.configResponse.Settings = []byte(`invalid-json`)
	assert.Error(t, s.Sync())
	remoteCli.configResponse.Settings = []byte(`{"uuid":"11111111-2222-3333-4444-555555555555","suspended":false}`)

	// 11. Environment variables override branch
	s.Config().EnvVars = environment.Variables{
		"TZ":         "UTC",
		"CUSTOM_KEY": "CUSTOM_VAL",
	}
	envVars := s.GetEnvironmentVariables()
	assert.Contains(t, envVars, "CUSTOM_KEY=CUSTOM_VAL")

	// install and internalInstall branches
	s.Config().SkipEggScripts = true
	remoteCli.installStatusErr = errors.New("cannot sync install state")
	_ = s.install(false)
	remoteCli.installStatusErr = nil
	s.Config().SkipEggScripts = false

	remoteCli.installScriptErr = errors.New("cannot get script")
	assert.Error(t, s.internalInstall())
	remoteCli.installScriptErr = nil

	remoteCli.scriptResponse = remote.InstallationScript{
		ContainerImage: "ghcr.io/pterodactyl/installers:debian",
		Entrypoint:     "bash",
		Script:         "echo 1",
	}
	s.installing.Store(true)
	assert.Error(t, s.internalInstall())
	s.installing.Store(false)

	// 12. Sync with 404 remote error
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	tsClient := remote.New(ts.URL)
	sWithTs, _ := New(tsClient)
	sWithTs.cfg.Uuid = s.ID()
	sWithTs.fs = s.fs
	err = sWithTs.Sync()
	assert.Error(t, err)
	assert.True(t, IsServerDoesNotExistError(err))

	// 13. CreateEnvironment and EnsureDataDirectoryExists
	machineIdDir := filepath.Join(tempDir, "machine_id")
	require.NoError(t, os.MkdirAll(machineIdDir, 0755))
	cfg.System.MachineID.Enable = true
	cfg.System.MachineID.Directory = machineIdDir
	config.Set(cfg)
	assert.NoError(t, s.CreateEnvironment())

	cfg.System.MachineID.Directory = filepath.Join(tempDir, "non_existent_subdir", "sub2")
	config.Set(cfg)
	assert.Error(t, s.CreateEnvironment())
	cfg.System.MachineID.Enable = false
	config.Set(cfg)

	assert.NoError(t, s.EnsureDataDirectoryExists())

	// EnsureDataDirectoryExists: directory does not exist, recreate successfully
	testNotExistPath := filepath.Join(tempDir, "new_dir_to_create")
	testFs, err := filesystem.New(testNotExistPath, 0, nil)
	require.NoError(t, err)
	_ = os.RemoveAll(testNotExistPath)
	s.fs = testFs
	assert.NoError(t, s.EnsureDataDirectoryExists()) // recreates directory

	// EnsureDataDirectoryExists: MkdirAll error (parent is immutable)
	immParent := filepath.Join(tempDir, "imm_mkdir_parent")
	require.NoError(t, os.Mkdir(immParent, 0755))
	mkdirFailDir := filepath.Join(immParent, "child")
	mkdirFailFs, err := filesystem.New(mkdirFailDir, 0, nil)
	require.NoError(t, err)
	_ = os.Remove(mkdirFailDir)
	if exec.Command("chattr", "+i", immParent).Run() == nil {
		s.fs = mkdirFailFs
		assert.Error(t, s.EnsureDataDirectoryExists())
		assert.Error(t, s.CreateEnvironment())
		_ = exec.Command("chattr", "-i", immParent).Run()
		s.fs = oldFs
	}

	// EnsureDataDirectoryExists: Lstat error that is not os.ErrNotExist (symlink loop parent)
	loopParent := filepath.Join(tempDir, "stat_loop_parent")
	loopFs, err := filesystem.New(filepath.Join(loopParent, "child"), 0, nil)
	require.NoError(t, err)
	_ = os.RemoveAll(loopParent)
	_ = os.Symlink(loopParent, loopParent)
	s.fs = loopFs
	assert.Error(t, s.EnsureDataDirectoryExists())
	_ = os.Remove(loopParent)

	s.fs = oldFs

	// 14. OnStateChange crash detection
	s.resources.State.Store(environment.ProcessStartingState)
	env.SetState(environment.ProcessOfflineState)
	env.setExitState(0, false, errors.New("exit error"))
	s.Config().CrashDetectionEnabled = true
	s.OnStateChange()
	time.Sleep(100 * time.Millisecond)

	// Too frequent crash
	s.crasher.SetLastCrash(time.Now())
	cfg.System.CrashDetection.Timeout = 60
	config.Set(cfg)
	s.resources.State.Store(environment.ProcessStartingState)
	env.setExitState(1, false, nil)
	s.OnStateChange()
	time.Sleep(100 * time.Millisecond)
	env.setExitState(0, false, nil)

	// 15. restoreEntry coverage
	hdrNoLink := &tar.Header{Typeflag: tar.TypeSymlink, Linkname: ""}
	fiSymlinkNoLink := &mockFileInfo{name: "sym_no_link", isDir: false, mode: os.ModeSymlink, sys: hdrNoLink}
	assert.NoError(t, s.restoreEntry("sym_no_link", fiSymlinkNoLink, strings.NewReader("")))

	fiSymlinkBadSys := &mockFileInfo{name: "sym_bad_sys", isDir: false, mode: os.ModeSymlink, sys: "not-a-header"}
	assert.NoError(t, s.restoreEntry("sym_bad_sys", fiSymlinkBadSys, strings.NewReader("")))

	fiNamedPipe := &mockFileInfo{name: "pipe", isDir: false, mode: os.ModeNamedPipe}
	assert.NoError(t, s.restoreEntry("pipe", fiNamedPipe, strings.NewReader("")))

	fiEscape := &mockFileInfo{name: "../../../escape.txt", isDir: false, mode: 0644, size: 5}
	assert.Error(t, s.restoreEntry("../../../escape.txt", fiEscape, strings.NewReader("hello")))

	// Backup failed notification
	failingBackup := backup.NewLocal(remoteCli, "invalid-ident!@#", "")
	remoteCli.setBackupStatusErr = errors.New("cannot send backup status")
	assert.Error(t, s.Backup(failingBackup))
	remoteCli.setBackupStatusErr = nil

	// 16. Config parser panic recovery
	cf := parser.ConfigurationFile{
		FileName: "server.properties",
		Parser:   parser.Properties,
		Replace: []parser.ConfigurationFileReplacement{
			{
				Match:       "server-port",
				ReplaceWith: parser.NewReplaceValue([]byte(`"bad\uZZZZ"`), jsonparser.String),
			},
		},
	}
	s.procConfig.ConfigurationFiles = []parser.ConfigurationFile{cf}
	require.NoError(t, os.WriteFile(filepath.Join(s.Filesystem().Path(), "server.properties"), []byte("server-port=25565\n"), 0644))
	s.UpdateConfigurationFiles()

	// 17. Mounts subLoop error
	subLoop := filepath.Join(loopPath, "child", "grandchild")
	_, err = resolveMountPath(subLoop, 1000)
	assert.Error(t, err)

	// 18. Manager PersistStates and ReadStates errors
	oldRoot := cfg.System.RootDirectory
	cfg.System.RootDirectory = "/dev/null/invalid"
	config.Set(cfg)
	assert.Error(t, mgr.PersistStates())
	_, err = mgr.ReadStates()
	assert.Error(t, err)
	cfg.System.RootDirectory = oldRoot
	config.Set(cfg)

	// 19. Manager InitServer
	rawResp := remote.ServerConfigurationResponse{
		Settings:             []byte(`{"uuid":"` + s.ID() + `","suspended":false}`),
		ProcessConfiguration: &remote.ProcessConfiguration{},
	}
	_, err = mgr.InitServer("22222222-2222-2222-2222-222222222222", rawResp)
	assert.Error(t, err)

	oldData := cfg.System.Data
	cfg.System.Data = "/dev/null/cannot_create"
	config.Set(cfg)
	_, err = mgr.InitServer(s.ID(), rawResp)
	assert.Error(t, err)
	cfg.System.Data = oldData
	config.Set(cfg)

	sInit, err := mgr.InitServer(s.ID(), rawResp)
	if err == nil {
		assert.NotNil(t, sInit)
		sInit.SyncWithEnvironment()
	}

	// 20. getServerwideIgnoredFiles size limit and invalid pattern
	_ = s.Filesystem().Delete(".pteroignore")
	bigIgnore := make([]byte, filesystem.MaxIgnoreLength+10)
	require.NoError(t, s.Filesystem().Writefile(".pteroignore", bytes.NewReader(bigIgnore)))
	ignored, err := s.getServerwideIgnoredFiles()
	assert.NoError(t, err)
	assert.Empty(t, ignored)

	_ = s.Filesystem().Delete(".pteroignore")
	invalidLine := strings.Repeat("*", 20)
	require.NoError(t, s.Filesystem().Writefile(".pteroignore", bytes.NewReader([]byte(invalidLine))))
	_, err = s.getServerwideIgnoredFiles()
	assert.Error(t, err)
	_ = s.Filesystem().Delete(".pteroignore")

	// 21. EnsureDataDirectoryExists
	assert.NoError(t, s.EnsureDataDirectoryExists())

	// 22. checkMountPathParents
	assert.NoError(t, checkMountPathParents("/tmp", 0))
}
