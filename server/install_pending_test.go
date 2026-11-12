package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

// Beginning an installation marks the server as installing, so power actions are
// refused, without taking the lock the installation process needs.
func TestBeginInstallDoesNotTakeTheInstallLock(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)
	assert.False(t, s.IsInstalling())

	release, err := s.beginInstall()
	require.NoError(t, err)

	assert.True(t, s.IsInstalling())
	assert.False(t, s.installing.Load(), "the lock belongs to InstallationProcess.Run")

	release()
	assert.False(t, s.IsInstalling())
}

func TestBeginInstallRejectsASecondInstallation(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	release, err := s.beginInstall()
	require.NoError(t, err)

	_, err = s.beginInstall()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot obtain installation lock")

	// Rejecting the second one must not end the first.
	assert.True(t, s.IsInstalling())
	release()
	assert.False(t, s.IsInstalling())

	// Once it is over another can begin.
	release, err = s.beginInstall()
	require.NoError(t, err)
	release()
}

// Regression test: Install and Reinstall used to set the installing state before
// running the installation process, which then failed to take the lock that was
// already held, so no installation ever completed.
func TestInstallationProcessRunsWhileTheInstallationIsBeginning(t *testing.T) {
	fake := &fakeInstallerDocker{logs: "installed successfully\n"}
	p := newInstallerTestProcess(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(config.Get().System.LogDirectory, "install"), 0o700))

	release, err := p.Server.beginInstall()
	require.NoError(t, err)

	require.NoError(t, p.Run())

	// The server is still busy until the installation as a whole is over, and the
	// lock of the process was released when it finished.
	assert.True(t, p.Server.IsInstalling())
	assert.False(t, p.Server.installing.Load())

	release()
	assert.False(t, p.Server.IsInstalling())
}
