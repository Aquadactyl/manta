//go:build darwin

package cmd

import (
	"context"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
)

var runMountCheck = environment.CheckDataMount

// startMountCheck checks in the background that containers can use the server
// data directory. The containers reach it through the file sharing of the Docker
// runtime, which fails in ways that otherwise only show up as lost data.
func startMountCheck(ctx context.Context) {
	if !config.Get().Docker.MountCheck {
		return
	}
	go runMountCheck(ctx)
}
