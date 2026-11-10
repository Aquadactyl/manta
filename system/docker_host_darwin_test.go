//go:build darwin

package system

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listen creates a unix socket at p. The path of a unix socket can not be longer
// than about 100 bytes, so the temporary directories of tests are too long.
func listen(t *testing.T, p string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	l, err := net.Listen("unix", p)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
}

func TestDarwinFindDockerSocket(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "m")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(home) })

	missing := filepath.Join(home, "missing.sock")

	// Nothing to find.
	assert.Empty(t, findDockerSocket(missing, home))

	// A socket of a runtime is found, in order of preference.
	colima := filepath.Join(home, ".colima", "default", "docker.sock")
	listen(t, colima)
	assert.Equal(t, colima, findDockerSocket(missing, home))

	orbstack := filepath.Join(home, ".orbstack", "run", "docker.sock")
	listen(t, orbstack)
	assert.Equal(t, orbstack, findDockerSocket(missing, home))

	// The default socket always wins, meaning the default is used.
	def := filepath.Join(home, "default.sock")
	listen(t, def)
	assert.Empty(t, findDockerSocket(def, home))

	// A regular file is not a socket.
	assert.False(t, isSocket(filepath.Join(home, ".orbstack")))
}

func TestDarwinDockerClientOptsRespectsDockerHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2375")
	assert.Len(t, DockerClientOpts(), 2)
}
