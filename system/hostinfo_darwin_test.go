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
