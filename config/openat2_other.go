// SPDX-License-Identifier: MIT

//go:build !linux

package config

import "errors"

// openat2Supported is false on every platform other than Linux, they use the
// openat syscall together with an explicit check of the resolved path.
const openat2Supported = false

// probeOpenat2 is never called when openat2Supported is false.
func probeOpenat2() error {
	return errors.New("openat2 is only available on Linux")
}
