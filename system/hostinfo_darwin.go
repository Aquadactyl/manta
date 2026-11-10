//go:build darwin

package system

import (
	dockersystem "github.com/docker/docker/api/types/system"
	"golang.org/x/sys/unix"
)

// hostKernelVersion returns the version of the Darwin kernel the host is running.
//
// The helper that is used on Linux runs system_profiler on macOS, which takes
// several seconds and is not needed to read a value the kernel already has.
func hostKernelVersion() (string, error) {
	return unix.Sysctl("kern.osrelease")
}

// hostOSName returns the name of the operating system the host is running, as
// it is shown in the panel.
func hostOSName(info dockersystem.Info) (string, error) {
	if v, err := unix.Sysctl("kern.osproductversion"); err == nil && v != "" {
		return "macOS " + v, nil
	}
	// The operating system of the Docker host is the Linux virtual machine on
	// macOS, which is not what is wanted here, so fall back to a generic name.
	return "macOS", nil
}
