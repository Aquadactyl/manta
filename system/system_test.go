package system

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetDockerInfo(t *testing.T) {
	ver, info, err := GetDockerInfo(context.Background())
	assert.NoError(t, err)
	assert.NotEmpty(t, ver.Version)
	assert.NotEmpty(t, info.Driver)

	// Test with canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = GetDockerInfo(ctx)
	assert.Error(t, err)
}

func TestGetSystemInformation(t *testing.T) {
	info, err := GetSystemInformation()
	assert.NoError(t, err)
	assert.NotNil(t, info)
	assert.NotEmpty(t, info.System.Architecture)
	assert.Greater(t, info.System.CPUThreads, 0)
	assert.NotEmpty(t, info.System.KernelVersion)
	assert.NotEmpty(t, info.System.OS)
	assert.NotEmpty(t, info.Docker.Version)
}
