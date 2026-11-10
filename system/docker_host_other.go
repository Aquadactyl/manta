//go:build !darwin

package system

import "github.com/docker/docker/client"

// DockerClientOpts returns the options used to create every Docker client.
func DockerClientOpts() []client.Opt {
	return []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
}
