//go:build linux

package system

import (
	"errors"
	"os"
	"testing"

	"github.com/docker/docker/pkg/parsers/kernel"
	"github.com/stretchr/testify/require"
)

func TestReadResourceUsageReturnsWhenCPUStatIsUnavailable(t *testing.T) {
	old := readProcStat
	readProcStat = func() ([]byte, error) { return nil, errors.New("proc stat unavailable") }
	t.Cleanup(func() { readProcStat = old })

	usage := ReadResourceUsage(t.Context(), t.TempDir())
	require.Nil(t, usage.CPUPercent)
	require.NotNil(t, usage.MemoryTotalBytes)
}

func TestHostKernelVersionPropagatesKernelReadError(t *testing.T) {
	old := readKernelVersion
	readKernelVersion = func() (*kernel.VersionInfo, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { readKernelVersion = old })

	_, err := hostKernelVersion()
	require.ErrorIs(t, err, os.ErrNotExist)
}
