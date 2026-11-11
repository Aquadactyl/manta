package environment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeChecker plays the part of Docker. What the container does with the mount
// is decided by run.
type fakeChecker struct {
	hasImage bool
	hasErr   error
	pullErr  error
	pulled   bool
	run      func(cfg *container.Config, host *container.HostConfig) (int64, error)

	cfg  *container.Config
	host *container.HostConfig
}

func (f *fakeChecker) HasImage(context.Context, string) (bool, error) { return f.hasImage, f.hasErr }
func (f *fakeChecker) PullImage(context.Context, string) error {
	f.pulled = true
	return f.pullErr
}
func (f *fakeChecker) Run(_ context.Context, cfg *container.Config, host *container.HostConfig) (int64, error) {
	f.cfg, f.host = cfg, host
	return f.run(cfg, host)
}

// writeThrough is a container that writes to a share the host sees.
func writeThrough(cfg *container.Config, host *container.HostConfig) (int64, error) {
	env := map[string]string{}
	for _, e := range cfg.Env {
		k, v, _ := strings.Cut(e, "=")
		env[k] = v
	}
	err := os.WriteFile(filepath.Join(host.Mounts[0].Source, env["MANTA_FILE"]), []byte(env["MANTA_TOKEN"]), 0o644)
	if err != nil {
		return 1, nil
	}
	return 0, nil
}

func TestCheckMountSucceedsAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	f := &fakeChecker{hasImage: true, run: writeThrough}

	res := CheckMount(context.Background(), f, dir, 501, 20)
	require.Equal(t, MountOK, res.Status, "%v", res.Err)
	assert.False(t, f.pulled, "an image that is there is not pulled")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "the marker file is removed")
}

// The container is a locked down copy of what a server gets: the same user, the
// directory mounted at a fixed place, and nothing else.
func TestCheckMountRunsAsTheServerUserWithoutPrivileges(t *testing.T) {
	dir := t.TempDir()
	f := &fakeChecker{hasImage: true, run: writeThrough}

	require.Equal(t, MountOK, CheckMount(context.Background(), f, dir, 501, 20).Status)

	assert.Equal(t, "501:20", f.cfg.User)
	assert.True(t, f.cfg.NetworkDisabled)
	assert.Equal(t, "none", string(f.host.NetworkMode))
	assert.Equal(t, []string{"ALL"}, []string(f.host.CapDrop))
	assert.True(t, f.host.ReadonlyRootfs)
	require.Len(t, f.host.Mounts, 1)
	assert.Equal(t, dir, f.host.Mounts[0].Source)
	assert.Equal(t, mountTarget, f.host.Mounts[0].Target)
	assert.False(t, f.host.Mounts[0].ReadOnly)
}

func TestCheckMountPullsAMissingImage(t *testing.T) {
	f := &fakeChecker{hasImage: false, run: writeThrough}

	res := CheckMount(context.Background(), f, t.TempDir(), 1, 1)
	assert.Equal(t, MountOK, res.Status)
	assert.True(t, f.pulled)
}

func TestCheckMountIsSkippedWhenTheImageCanNotBePulled(t *testing.T) {
	f := &fakeChecker{pullErr: errors.New("no route to host"), run: writeThrough}

	res := CheckMount(context.Background(), f, t.TempDir(), 1, 1)
	assert.Equal(t, MountSkipped, res.Status)
	assert.ErrorContains(t, res.Err, "no route to host")
	assert.Nil(t, f.cfg, "no container is run")
}

func TestCheckMountIsSkippedForAMissingDirectory(t *testing.T) {
	f := &fakeChecker{hasImage: true, run: writeThrough}

	res := CheckMount(context.Background(), f, filepath.Join(t.TempDir(), "missing"), 1, 1)
	assert.Equal(t, MountSkipped, res.Status)
}

func TestCheckMountReportsARefusedMount(t *testing.T) {
	f := &fakeChecker{hasImage: true, run: func(*container.Config, *container.HostConfig) (int64, error) {
		return 0, errors.New("Mounts denied: the path is not shared from the host")
	}}

	res := CheckMount(context.Background(), f, t.TempDir(), 1, 1)
	assert.Equal(t, MountRefused, res.Status)
	assert.ErrorContains(t, res.Err, "not shared")
	assert.Contains(t, res.Message("/data"), "file sharing")
}

func TestCheckMountReportsAReadOnlyMount(t *testing.T) {
	f := &fakeChecker{hasImage: true, run: func(*container.Config, *container.HostConfig) (int64, error) {
		return 1, nil // sh: can't create file: Read-only file system
	}}

	res := CheckMount(context.Background(), f, t.TempDir(), 1, 1)
	assert.Equal(t, MountNotWritable, res.Status)
	assert.Contains(t, res.Message("/data"), "writable")
}

// A runtime that does not share the directory gives the container a private
// copy, so the write succeeds and nothing arrives on the host.
func TestCheckMountReportsADirectoryThatIsNotShared(t *testing.T) {
	f := &fakeChecker{hasImage: true, run: func(*container.Config, *container.HostConfig) (int64, error) {
		return 0, nil
	}}

	res := CheckMount(context.Background(), f, t.TempDir(), 1, 1)
	assert.Equal(t, MountNotShared, res.Status)
	assert.Contains(t, res.Message("/data"), "private copy")
}

// A file with other contents is not the one that was written.
func TestCheckMountReportsAMarkerWithTheWrongContent(t *testing.T) {
	f := &fakeChecker{hasImage: true, run: func(cfg *container.Config, host *container.HostConfig) (int64, error) {
		name := strings.TrimPrefix(cfg.Env[1], "MANTA_FILE=")
		return 0, os.WriteFile(filepath.Join(host.Mounts[0].Source, name), []byte("something else"), 0o644)
	}}

	res := CheckMount(context.Background(), f, t.TempDir(), 1, 1)
	assert.Equal(t, MountNotShared, res.Status)
}

func TestCheckMountUsesADifferentMarkerEveryTime(t *testing.T) {
	var names []string
	f := &fakeChecker{hasImage: true, run: func(cfg *container.Config, host *container.HostConfig) (int64, error) {
		names = append(names, cfg.Env[1])
		return writeThrough(cfg, host)
	}}
	dir := t.TempDir()

	CheckMount(context.Background(), f, dir, 1, 1)
	CheckMount(context.Background(), f, dir, 1, 1)
	require.Len(t, names, 2)
	assert.NotEqual(t, names[0], names[1])
}

func TestMountCheckMessages(t *testing.T) {
	for _, s := range []MountCheckStatus{MountOK, MountSkipped, MountRefused, MountNotWritable, MountNotShared} {
		assert.NotEmpty(t, MountCheckResult{Status: s, Err: errors.New("x")}.Message("/data"))
	}
}
