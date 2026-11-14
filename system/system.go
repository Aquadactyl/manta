package system

import (
	"context"
	"runtime"

	dockersystem "github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
)

type Information struct {
	Version      string            `json:"version"`
	Docker       DockerInformation `json:"docker"`
	System       System            `json:"system"`
	Capabilities map[string]bool   `json:"capabilities"`
}

type DockerInformation struct {
	Version    string           `json:"version"`
	Cgroups    DockerCgroups    `json:"cgroups"`
	Containers DockerContainers `json:"containers"`
	Storage    DockerStorage    `json:"storage"`
	Runc       DockerRunc       `json:"runc"`
}

type DockerCgroups struct {
	Driver  string `json:"driver"`
	Version string `json:"version"`
}

type DockerContainers struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Paused  int `json:"paused"`
	Stopped int `json:"stopped"`
}

type DockerStorage struct {
	Driver     string `json:"driver"`
	Filesystem string `json:"filesystem"`
}

type DockerRunc struct {
	Version string `json:"version"`
}

type System struct {
	Architecture  string `json:"architecture"`
	CPUThreads    int    `json:"cpu_threads"`
	MemoryBytes   int64  `json:"memory_bytes"`
	KernelVersion string `json:"kernel_version"`
	OS            string `json:"os"`
	OSType        string `json:"os_type"`
}

var (
	systemHostKernelVersion = hostKernelVersion
	systemGetDockerInfo     = GetDockerInfo
	systemHostOSName        = hostOSName
)

func GetSystemInformation() (*Information, error) {
	kernelVersion, err := systemHostKernelVersion()
	if err != nil {
		return nil, err
	}

	version, info, err := systemGetDockerInfo(context.Background())
	if err != nil {
		return nil, err
	}

	os, err := systemHostOSName(info)
	if err != nil {
		return nil, err
	}

	var filesystem string
	for _, v := range info.DriverStatus {
		if v[0] != "Backing Filesystem" {
			continue
		}
		filesystem = v[1]
		break
	}

	return &Information{
		Capabilities: map[string]bool{"system_usage": HostUsageSupported},
		Version:      Version,
		Docker: DockerInformation{
			Version: version.Version,
			Cgroups: DockerCgroups{
				Driver:  info.CgroupDriver,
				Version: info.CgroupVersion,
			},
			Containers: DockerContainers{
				Total:   info.Containers,
				Running: info.ContainersRunning,
				Paused:  info.ContainersPaused,
				Stopped: info.ContainersStopped,
			},
			Storage: DockerStorage{
				Driver:     info.Driver,
				Filesystem: filesystem,
			},
			Runc: DockerRunc{
				Version: info.RuncCommit.ID,
			},
		},
		System: System{
			Architecture:  runtime.GOARCH,
			CPUThreads:    runtime.NumCPU(),
			MemoryBytes:   info.MemTotal,
			KernelVersion: kernelVersion,
			OS:            os,
			OSType:        runtime.GOOS,
		},
	}, nil
}

// GetDockerInfo returns the version and system info from the host Docker daemon.
func GetDockerInfo(ctx context.Context) (dockersystem.VersionResponse, dockersystem.Info, error) {
	c, err := client.New(DockerClientOpts()...)
	if err != nil {
		return dockersystem.VersionResponse{}, dockersystem.Info{}, err
	}
	defer c.Close()

	dockerVersion, err := c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return dockersystem.VersionResponse{}, dockersystem.Info{}, err
	}

	dockerInfo, err := c.Info(ctx, client.InfoOptions{})
	if err != nil {
		return dockersystem.VersionResponse{}, dockersystem.Info{}, err
	}

	return dockersystem.VersionResponse{
		Platform:      dockersystem.PlatformInfo{Name: dockerVersion.Platform.Name},
		Version:       dockerVersion.Version,
		APIVersion:    dockerVersion.APIVersion,
		MinAPIVersion: dockerVersion.MinAPIVersion,
		Os:            dockerVersion.Os,
		Arch:          dockerVersion.Arch,
		Experimental:  dockerInfo.Info.ExperimentalBuild,
		Components:    dockerVersion.Components,
	}, dockerInfo.Info, nil
}
