package environment

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type legacyLogEnvironment struct {
	ProcessEnvironment
	called bool
}

func (e *legacyLogEnvironment) Readlog(lines int) ([]string, error) {
	e.called = true
	return []string{"legacy log"}, nil
}

func TestReadLogsPreservesLegacyEnvironments(t *testing.T) {
	e := &legacyLogEnvironment{}
	logs, err := ReadLogs(context.Background(), e, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"legacy log"}, logs)
	require.True(t, e.called)
	e.called = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ReadLogs(ctx, e, 10)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, e.called)
}
