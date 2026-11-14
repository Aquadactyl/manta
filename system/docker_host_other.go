//go:build !darwin

package system

import "github.com/moby/moby/client"

// DockerClientOpts returns the options used to create every Docker client.
func DockerClientOpts() []client.Opt {
	return []client.Opt{client.FromEnv}
}
