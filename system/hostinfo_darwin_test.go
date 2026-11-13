//go:build darwin

package system

import (
	"errors"
	"testing"

	dockersystem "github.com/docker/docker/api/types/system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestDarwinHostInfo(t *testing.T) {
	kernel, err := hostKernelVersion()
	require.NoError(t, err)
	assert.NotEmpty(t, kernel)

	name, err := hostOSName(dockersystem.Info{OperatingSystem: "Docker Desktop"})
	require.NoError(t, err)
	assert.Contains(t, name, "macOS")
	assert.NotContains(t, name, "Docker Desktop")
}

func TestDarwinHostKernelVersionFallbacks(t *testing.T) {
	oldSysctl, oldUname := sysctlDarwin, unameDarwin
	t.Cleanup(func() { sysctlDarwin, unameDarwin = oldSysctl, oldUname })

	sysctlDarwin = func(string) (string, error) { return "24.1.0", nil }
	version, err := hostKernelVersion()
	require.NoError(t, err)
	assert.Equal(t, "24.1.0", version)

	sysctlDarwin = func(string) (string, error) { return "", nil }
	unameDarwin = func(u *unix.Utsname) error { copy(u.Release[:], []byte("24.2.0")); return nil }
	version, err = hostKernelVersion()
	require.NoError(t, err)
	assert.Equal(t, "24.2.0", version)

	sysctlDarwin = func(string) (string, error) { return "", errors.New("sysctl failed") }
	unameDarwin = func(*unix.Utsname) error { return errors.New("uname failed") }
	_, err = hostKernelVersion()
	require.Error(t, err)
}

func TestDarwinHostOSNameUsesSystemValues(t *testing.T) {
	oldSysctl, oldUname := sysctlDarwin, unameDarwin
	t.Cleanup(func() { sysctlDarwin, unameDarwin = oldSysctl, oldUname })
	sysctlDarwin = func(name string) (string, error) {
		switch name {
		case "kern.osproductversion":
			return "26.0", nil
		case "kern.osversion":
			return "A123", nil
		case "kern.osrelease":
			return "25.0.0", nil
		default:
			return "", errors.New("unexpected sysctl")
		}
	}
	name, err := hostOSName(dockersystem.Info{OperatingSystem: "Linux VM"})
	require.NoError(t, err)
	assert.Equal(t, "macOS 26.0 (A123)", name)
}

func TestDarwinFormatOSName(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		version, build, darwin string
		want                   string
	}{
		{"everything", "15.1", "24B83", "24.1.0", "macOS 15.1 (24B83)"},
		{"no build", "15.1", "", "24.1.0", "macOS 15.1"},
		{"only darwin", "", "", "24.1.0", "macOS (Darwin 24.1.0)"},
		{"nothing", "", "", "", "macOS"},
		{"build without version", "", "24B83", "24.1.0", "macOS (Darwin 24.1.0)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatOSName(tt.version, tt.build, tt.darwin))
		})
	}
}
