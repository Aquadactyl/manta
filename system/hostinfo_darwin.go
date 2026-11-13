//go:build darwin

package system

import (
	dockersystem "github.com/docker/docker/api/types/system"
	"golang.org/x/sys/unix"
)

var (
	sysctlDarwin = unix.Sysctl
	unameDarwin  = unix.Uname
)

// hostKernelVersion returns the version of the Darwin kernel the host is running.
//
// The helper that is used on Linux runs system_profiler on macOS, which takes
// several seconds and is not needed to read a value the kernel already has.
func hostKernelVersion() (string, error) {
	if v, err := sysctlDarwin("kern.osrelease"); err == nil && v != "" {
		return v, nil
	}

	var u unix.Utsname
	if err := unameDarwin(&u); err != nil {
		return "", err
	}
	return unix.ByteSliceToString(u.Release[:]), nil
}

// hostOSName returns the name of the operating system the host is running, as
// it is shown in the panel, for example "macOS 15.1 (24B83)".
//
// The operating system of the Docker host is the Linux virtual machine on
// macOS, which is not what is wanted here, so it is not used.
func hostOSName(_ dockersystem.Info) (string, error) {
	version, _ := sysctlDarwin("kern.osproductversion")
	build, _ := sysctlDarwin("kern.osversion")
	darwin, _ := hostKernelVersion()

	return formatOSName(version, build, darwin), nil
}

// formatOSName builds the name from whatever could be read, preferring the
// product version, which is what users know, over the Darwin version.
func formatOSName(version, build, darwin string) string {
	switch {
	case version != "" && build != "":
		return "macOS " + version + " (" + build + ")"
	case version != "":
		return "macOS " + version
	case darwin != "":
		return "macOS (Darwin " + darwin + ")"
	default:
		return "macOS"
	}
}
