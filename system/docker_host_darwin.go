//go:build darwin

package system

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/apex/log"
	"github.com/docker/docker/client"
)

// DockerClientOpts returns the options used to create every Docker client.
//
// macOS has no Docker daemon of its own. Docker Desktop, OrbStack, Colima and
// Rancher Desktop each run one in a virtual machine and expose a socket in the
// home directory of the user, so that is looked for when neither DOCKER_HOST
// nor the conventional /var/run/docker.sock are available.
func DockerClientOpts() []client.Opt {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if os.Getenv("DOCKER_HOST") == "" {
		if host := dockerHostFallback(); host != "" {
			opts = append(opts, client.WithHost(host))
		}
	}
	return opts
}

var dockerHostLogOnce sync.Once

// dockerHostFallback returns the address of the first Docker socket found in a
// well known location, or an empty string if the default one should be used.
func dockerHostFallback() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	socket := findDockerSocket("/var/run/docker.sock", home)
	if socket == "" {
		return ""
	}
	dockerHostLogOnce.Do(func() {
		log.WithField("socket", socket).Info("/var/run/docker.sock not found, using the Docker socket in the home directory")
	})
	return "unix://" + socket
}

// findDockerSocket returns the path of the first Docker socket in the home
// directory, unless the default socket exists, in which case it returns an
// empty string.
func findDockerSocket(defaultSocket, home string) string {
	if isSocket(defaultSocket) {
		return ""
	}
	for _, p := range []string{
		".docker/run/docker.sock",   // Docker Desktop
		".orbstack/run/docker.sock", // OrbStack
		".colima/default/docker.sock",
		".rd/docker.sock", // Rancher Desktop
	} {
		if socket := filepath.Join(home, p); isSocket(socket) {
			return socket
		}
	}
	return ""
}

func isSocket(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode()&os.ModeSocket != 0
}
