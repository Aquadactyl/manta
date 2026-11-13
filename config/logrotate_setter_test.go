package config

import "testing"

func TestSetLogrotateDirectory(t *testing.T) {
	previous := logrotateDirectory
	t.Cleanup(func() { logrotateDirectory = previous })
	SetLogrotateDirectory(t.TempDir())
	if logrotateDirectory == previous {
		t.Fatal("SetLogrotateDirectory did not update the directory")
	}
}
