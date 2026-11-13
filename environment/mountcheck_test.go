package environment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
)

type mountCheckStub struct {
	image       bool
	imageErr    error
	pullErr     error
	runCode     int64
	runErr      error
	writeMarker bool
	markerValue string
}

func (m *mountCheckStub) HasImage(context.Context, string) (bool, error) { return m.image, m.imageErr }
func (m *mountCheckStub) PullImage(context.Context, string) error        { return m.pullErr }
func (m *mountCheckStub) Run(_ context.Context, cfg *container.Config, host *container.HostConfig) (int64, error) {
	if m.writeMarker {
		var name string
		var token string
		for _, env := range cfg.Env {
			if strings.HasPrefix(env, "MANTA_FILE=") {
				name = strings.TrimPrefix(env, "MANTA_FILE=")
			}
			if strings.HasPrefix(env, "MANTA_TOKEN=") {
				token = strings.TrimPrefix(env, "MANTA_TOKEN=")
			}
		}
		value := token
		if m.markerValue != "" {
			value = m.markerValue
		}
		if len(host.Mounts) > 0 {
			_ = os.WriteFile(filepath.Join(host.Mounts[0].Source, name), []byte(value), 0o600)
		}
	}
	return m.runCode, m.runErr
}

func TestCheckMountOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		stub    mountCheckStub
		status  MountCheckStatus
		wantErr bool
	}{
		{name: "missing directory", status: MountSkipped, wantErr: true},
		{name: "not a directory", status: MountSkipped, wantErr: true},
		{name: "image lookup failure and pull failure", stub: mountCheckStub{imageErr: errors.New("inspect failed"), pullErr: errors.New("pull failed")}, status: MountSkipped, wantErr: true},
		{name: "pull missing image", stub: mountCheckStub{pullErr: errors.New("pull failed")}, status: MountSkipped, wantErr: true},
		{name: "docker refused mount", stub: mountCheckStub{runErr: errors.New("mount refused")}, status: MountRefused, wantErr: true},
		{name: "container cannot write", stub: mountCheckStub{runCode: 1}, status: MountNotWritable, wantErr: true},
		{name: "marker is not shared", stub: mountCheckStub{}, status: MountNotShared, wantErr: true},
		{name: "marker has wrong contents", stub: mountCheckStub{writeMarker: true, markerValue: "wrong"}, status: MountNotShared},
		{name: "shared and writable", stub: mountCheckStub{image: true, writeMarker: true}, status: MountOK},
		{name: "pull then succeeds", stub: mountCheckStub{writeMarker: true}, status: MountOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.name == "missing directory" {
				dir = filepath.Join(dir, "missing")
			}
			if tt.name == "not a directory" {
				file := filepath.Join(dir, "file")
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				dir = file
			}
			got := CheckMount(context.Background(), &tt.stub, dir, 1000, 1000)
			if got.Status != tt.status {
				t.Fatalf("status = %v, want %v (err: %v)", got.Status, tt.status, got.Err)
			}
			if tt.wantErr != (got.Err != nil) {
				t.Fatalf("err = %v, wantErr %v", got.Err, tt.wantErr)
			}
			if tt.status == MountOK {
				if got.Message(dir) == "" {
					t.Fatal("success message is empty")
				}
			}
		})
	}
}

func TestCheckMountSkipsWhenRandomTokenCannotBeGenerated(t *testing.T) {
	previous := readMountCheckRandom
	readMountCheckRandom = func([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
	t.Cleanup(func() { readMountCheckRandom = previous })

	result := CheckMount(context.Background(), &mountCheckStub{image: true}, t.TempDir(), 0, 0)
	if result.Status != MountSkipped || result.Err == nil || !strings.Contains(result.Err.Error(), "entropy unavailable") {
		t.Fatalf("CheckMount() = %#v, want skipped entropy error", result)
	}
}

func TestMountCheckMessages(t *testing.T) {
	for _, status := range []MountCheckStatus{MountSkipped, MountRefused, MountNotWritable, MountNotShared, MountCheckStatus(99)} {
		if msg := (MountCheckResult{Status: status, Err: errors.New("example")}).Message("/data"); msg == "" {
			t.Fatalf("empty message for status %d", status)
		}
	}
}
