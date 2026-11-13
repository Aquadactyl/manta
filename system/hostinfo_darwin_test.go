//go:build darwin

package system

import (
	"testing"

	dockersystem "github.com/docker/docker/api/types/system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
