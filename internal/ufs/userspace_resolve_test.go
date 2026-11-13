//go:build unix

package ufs

import "os"

// Setting UFS_TEST_USERSPACE_RESOLVE=1 runs the whole test suite on the path
// resolver macOS uses, so it is held to everything the kernel's is.
func init() {
	if os.Getenv("UFS_TEST_USERSPACE_RESOLVE") == "1" {
		forceUserspaceResolve = true
	}
}
