package environment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apex/log"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"

	"github.com/pterodactyl/wings/config"
)

// mountCheckImage is the image the check runs in. It is the installer image the
// panel uses for most servers, so it is pulled for them anyway, and it is small.
const mountCheckImage = "ghcr.io/pterodactyl/installers:alpine"

// mountTarget is where the data directory appears in the check container.
const mountTarget = "/mnt/data"

// MountCheckStatus is the outcome of a check of the data directory mount.
type MountCheckStatus int

const (
	// MountOK means containers can write to the data directory and the host sees
	// what they write.
	MountOK MountCheckStatus = iota
	// MountSkipped means the check could not be run, for example because the
	// image is unavailable while offline. It says nothing about the mount.
	MountSkipped
	// MountRefused means Docker refused to mount the directory at all.
	MountRefused
	// MountNotWritable means the container started but could not write to it.
	MountNotWritable
	// MountNotShared means the container could write, but what it wrote is not in
	// the directory on the host: the runtime does not share that directory, and
	// gave the container a private copy that is lost when it is removed.
	MountNotShared
)

// MountCheckResult describes the outcome of a check.
type MountCheckResult struct {
	Status MountCheckStatus
	// Err is the underlying error, if there is one.
	Err error
}

// Message returns what to tell the person running the daemon.
func (r MountCheckResult) Message(dir string) string {
	switch r.Status {
	case MountOK:
		return "the server data directory is shared with the Docker runtime and writable by containers"
	case MountRefused:
		return fmt.Sprintf("the Docker runtime refused to mount the server data directory %q (%v): add it to the file sharing settings of the runtime, or move the data directory somewhere the runtime shares", dir, r.Err)
	case MountNotWritable:
		return fmt.Sprintf("containers can not write to the server data directory %q: with Colima or Rancher Desktop the mount is read-only unless made writable (in Colima set 'writable: true' on the mount that contains it), and otherwise check its permissions, servers will fail to start until this is fixed", dir)
	case MountNotShared:
		return fmt.Sprintf("the server data directory %q is not shared with the Docker runtime: containers write to a private copy inside the virtual machine and nothing reaches the host, so server data is lost when they are removed, add the directory to the file sharing of the runtime and restart it", dir)
	default:
		return "the server data directory mount could not be checked"
	}
}

// mountChecker is what the check needs from Docker.
type mountChecker interface {
	// HasImage reports whether the image is available locally.
	HasImage(ctx context.Context, ref string) (bool, error)
	// PullImage pulls the image.
	PullImage(ctx context.Context, ref string) error
	// Run runs a container to completion, removes it, and returns its exit code.
	Run(ctx context.Context, cfg *container.Config, host *container.HostConfig) (int64, error)
}

// CheckDataMount runs the check against the Docker runtime and logs the result.
// It is meant to be run in the background, as pulling the image can take a while.
func CheckDataMount(ctx context.Context) {
	cli, err := Docker()
	if err != nil {
		log.WithError(err).Debug("skipping the server data directory mount check")
		return
	}

	cfg := config.Get()
	dir := cfg.System.Data
	res := CheckMount(ctx, &dockerMountChecker{cli: cli}, dir, cfg.System.User.Uid, cfg.System.User.Gid)

	l := log.WithField("path", dir)
	switch res.Status {
	case MountOK:
		l.Info(res.Message(dir))
	case MountSkipped:
		l.WithError(res.Err).Info(res.Message(dir))
	default:
		l.WithError(res.Err).Warn(res.Message(dir))
	}
}

// CheckMount checks that containers can write to dir and that the files they
// write are the files on the host, running as the given user like servers do.
func CheckMount(ctx context.Context, mc mountChecker, dir string, uid, gid int) MountCheckResult {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return MountCheckResult{Status: MountSkipped, Err: fmt.Errorf("%s is not a directory", dir)}
	}

	pullCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if ok, err := mc.HasImage(pullCtx, mountCheckImage); err != nil || !ok {
		if err := mc.PullImage(pullCtx, mountCheckImage); err != nil {
			return MountCheckResult{Status: MountSkipped, Err: err}
		}
	}

	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return MountCheckResult{Status: MountSkipped, Err: err}
	}
	token := hex.EncodeToString(tok)
	name := ".manta-mount-check-" + token[:8]
	marker := filepath.Join(dir, name)
	// The marker is removed whatever happens.
	defer os.Remove(marker)

	cfg := &container.Config{
		Image:           mountCheckImage,
		User:            fmt.Sprintf("%d:%d", uid, gid),
		Env:             []string{"MANTA_TOKEN=" + token, "MANTA_FILE=" + name},
		Cmd:             []string{"sh", "-c", `printf '%s' "$MANTA_TOKEN" > "` + mountTarget + `/$MANTA_FILE"`},
		NetworkDisabled: true,
		Labels:          map[string]string{"uk.aquadactyl.manta.check": "mount"},
	}
	pids := int64(32)
	host := &container.HostConfig{
		Mounts:         []mount.Mount{{Type: mount.TypeBind, Source: dir, Target: mountTarget}},
		NetworkMode:    "none",
		CapDrop:        []string{"ALL"},
		ReadonlyRootfs: true,
		SecurityOpt:    []string{"no-new-privileges"},
		Resources:      container.Resources{Memory: 64 << 20, PidsLimit: &pids},
	}

	runCtx, cancelRun := context.WithTimeout(ctx, time.Minute)
	defer cancelRun()
	code, err := mc.Run(runCtx, cfg, host)
	switch {
	case err != nil:
		return MountCheckResult{Status: MountRefused, Err: err}
	case code != 0:
		return MountCheckResult{Status: MountNotWritable, Err: fmt.Errorf("the check exited with status %d", code)}
	}

	got, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(got)) != token {
		return MountCheckResult{Status: MountNotShared, Err: err}
	}
	return MountCheckResult{Status: MountOK}
}

// dockerMountChecker runs the check against a real Docker client.
type dockerMountChecker struct {
	cli *client.Client
}

func (d *dockerMountChecker) HasImage(ctx context.Context, ref string) (bool, error) {
	if _, err := d.cli.ImageInspect(ctx, ref); err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (d *dockerMountChecker) PullImage(ctx context.Context, ref string) error {
	out, err := d.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return err
	}
	defer out.Close()
	// The pull only finishes once the stream has been read to the end.
	_, err = io.Copy(io.Discard, out)
	return err
}

func (d *dockerMountChecker) Run(ctx context.Context, cfg *container.Config, host *container.HostConfig) (int64, error) {
	created, err := d.cli.ContainerCreate(ctx, cfg, host, nil, nil, "")
	if err != nil {
		return 0, err
	}
	// Removing must not depend on the context of the run, which may have expired.
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = d.cli.ContainerRemove(rctx, created.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	}()

	if err := d.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return 0, err
	}

	statusCh, errCh := d.cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		return 0, err
	case st := <-statusCh:
		if st.Error != nil {
			return 0, fmt.Errorf("%s", st.Error.Message)
		}
		return st.StatusCode, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
