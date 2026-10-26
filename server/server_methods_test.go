package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/internal/models"
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

