package server

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
)

func TestConsoleStartupMarkersAndInputOwnership(t *testing.T) {
	setTestConfig(t)
	for _, tc := range []struct {
		name, line     string
		strip, started bool
	}{
		{"plain", "Server Done", false, true},
		{"ANSI stripped", "Server D\x1b[32mone", true, true},
		{"ANSI retained", "Server D\x1b[32mone", false, false},
		{"unmatched", "Still loading", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(nil)
			require.NoError(t, err)
			t.Cleanup(s.CleanupForDestroy)
			env := &stateEnvironment{state: environment.ProcessStartingState}
			s.Environment = env
			process := &remote.ProcessConfiguration{}
			require.NoError(t, json.Unmarshal([]byte(`{"startup":{"done":["Done"]}}`), process))
			process.Startup.StripAnsi = tc.strip
			s.procConfig = process
			line := []byte(tc.line)
			original := bytes.Clone(line)
			s.onConsoleOutput(line)
			require.Equal(t, original, line)
			require.Equal(t, tc.started, env.State() == environment.ProcessRunningState)
		})
	}
}

func TestRunningConsoleDoesNotWaitForProcessConfiguration(t *testing.T) {
	setTestConfig(t)
	s, err := New(nil)
	require.NoError(t, err)
	t.Cleanup(s.CleanupForDestroy)
	s.Environment = &stateEnvironment{state: environment.ProcessRunningState}
	s.Lock()
	defer s.Unlock()
	done := make(chan struct{})
	go func() { s.onConsoleOutput([]byte("already running")); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("running console inspection waited for unrelated process configuration")
	}
}
