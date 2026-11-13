// SPDX-License-Identifier: MIT

//go:build linux

package config

import "golang.org/x/sys/unix"

var configOpenat2 = unix.Openat2

// openat2Supported reports whether the platform can provide the openat2
// syscall at all. The running kernel may still be too old, see probeOpenat2.
const openat2Supported = true

// probeOpenat2 checks whether the running kernel supports the openat2 syscall,
// which was added in Linux 5.6.
func probeOpenat2() error {
	fd, err := configOpenat2(unix.AT_FDCWD, "/", &unix.OpenHow{})
	if err != nil {
		return err
	}
	_ = unix.Close(fd)
	return nil
}
