//go:build !darwin

package system

import (
	"github.com/acobaugh/osrelease"
	dockersystem "github.com/docker/docker/api/types/system"
	"github.com/docker/docker/pkg/parsers/kernel"
)

// hostKernelVersion returns the version of the kernel the host is running.
func hostKernelVersion() (string, error) {
	k, err := kernel.GetKernelVersion()
	if err != nil {
		return "", err
	}
	return k.String(), nil
}

// hostOSName returns the name of the operating system the host is running, as
// it is shown in the panel.
func hostOSName(info dockersystem.Info) (string, error) {
	release, err := osrelease.Read()
	if err != nil {
		return "", err
	}
	switch {
	case release["PRETTY_NAME"] != "":
		return release["PRETTY_NAME"], nil
	case release["NAME"] != "":
		return release["NAME"], nil
	default:
		return info.OperatingSystem, nil
	}
}
