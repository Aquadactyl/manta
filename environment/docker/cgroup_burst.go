package docker

import (
	"context"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/moby/moby/client"

	"github.com/Aquadactyl/manta/config"
)

// cgroupV2 reports whether the host uses the unified cgroup v2 hierarchy.
var cgroupV2 = sync.OnceValue(func() bool {
	_, err := os.Stat("/sys/fs/cgroup/cgroup.controllers")
	return err == nil
})

var burstWarning sync.Once

var readCgroupFile = os.ReadFile
var writeCgroupBurstFile = os.WriteFile
var writeBurstFileForProcess = writeBurstFile

// cpuBurstMicroseconds returns the burst allowance in microseconds for the given
// CFS quota and configured percentage. The kernel rejects a burst larger than the
// quota, so the value is clamped to it.
func cpuBurstMicroseconds(quota int64, percent int64) int64 {
	if quota <= 0 || percent <= 0 {
		return 0
	}
	if percent > 100 {
		percent = 100
	}
	return quota * percent / 100
}

// resolveCgroupCPUFile parses the contents of a /proc/<pid>/cgroup file and
// returns the absolute path of the CFS burst file for that process's cgroup.
func resolveCgroupCPUFile(procCgroup string, v2 bool) (string, error) {
	for _, line := range strings.Split(procCgroup, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 || !strings.HasPrefix(parts[2], "/") || strings.Contains(parts[2], "..") {
			continue
		}
		if v2 {
			if parts[0] == "0" && parts[1] == "" {
				return path.Join("/sys/fs/cgroup", parts[2], "cpu.max.burst"), nil
			}
			continue
		}
		for _, controller := range strings.Split(parts[1], ",") {
			if controller == "cpu" {
				return path.Join("/sys/fs/cgroup/cpu", parts[2], "cpu.cfs_burst_us"), nil
			}
		}
	}
	return "", errors.New("environment/docker: no cpu controller found in cgroup file")
}

// writeCPUBurst writes a burst value in microseconds into the cpu cgroup of the
// given process. This is expected to fail on kernels older than 5.14 or when the
// cgroup hierarchy is not writable by Wings, so failures are only logged.
func writeCPUBurst(l *log.Entry, pid int, burst int64) {
	writeCPUBurstForPlatform(l, pid, burst, runtime.GOOS)
}

func writeCPUBurstForPlatform(l *log.Entry, pid int, burst int64, goos string) {
	// Only Linux has the cgroup hierarchy. Elsewhere the container runs in a
	// virtual machine whose processes can not be reached through the host.
	if pid <= 0 || goos != "linux" {
		return
	}
	if err := writeBurstFileForProcess(pid, burst); err != nil {
		logBurstFailure(l.WithField("error", err), burst)
		return
	}
	l.WithField("burst_us", burst).Debug("updated container cpu burst")
}

func writeBurstFile(pid int, burst int64) error {
	b, err := readCgroupFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return err
	}
	f, err := resolveCgroupCPUFile(string(b), cgroupV2())
	if err != nil {
		return err
	}
	return writeCgroupBurstFile(f, []byte(strconv.FormatInt(burst, 10)), 0o644) //nolint:gosec // an existing cgroupfs file, the mode is never applied
}

// logBurstFailure warns the first time a burst cannot be applied and stays at
// debug otherwise. Failed clears are always quiet since a host that never
// accepted a burst has nothing to clear.
func logBurstFailure(l *log.Entry, burst int64) {
	if burst > 0 {
		first := false
		burstWarning.Do(func() { first = true })
		if first {
			l.Warn("failed to set cpu burst, this requires Linux 5.14 or newer and a writable cgroup hierarchy")
			return
		}
	}
	l.Debug("failed to set cpu burst")
}

// SetCPUBurst applies the configured CFS burst to a running container based on
// the CFS quota in microseconds it was created with. This is a no-op when
// bursting is disabled or the container has no CPU limit.
func SetCPUBurst(ctx context.Context, cli *client.Client, containerID string, quota int64) {
	cfg := config.Get().Docker.CPUBurst
	if !cfg.Enabled || quota <= 0 {
		return
	}
	result, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	c := result.Container
	if err != nil || c.State == nil {
		return
	}
	writeCPUBurst(log.WithField("container_id", containerID), c.State.Pid, cpuBurstMicroseconds(quota, cfg.Percent))
}

// applyCPUBurst applies the configured CFS burst to the environment's container
// using its current CPU limit.
func (e *Environment) applyCPUBurst(ctx context.Context) {
	quota := e.Configuration.Limits().CPULimit * config.Get().Docker.CPUPeriodMicroseconds() / 100
	SetCPUBurst(ctx, e.client, e.ID, quota)
}

// clearCPUBurst zeroes the CFS burst for the given container process. This must
// happen before a quota change is applied since the kernel rejects a quota lower
// than the current burst. It runs even when bursting is disabled so a value set
// before the feature was turned off cannot block future quota changes.
func (e *Environment) clearCPUBurst(pid int) {
	writeCPUBurst(e.log(), pid, 0)
}
