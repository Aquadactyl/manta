//go:build darwin

package filesystem

import "syscall"

// statWithCtime returns a stat result with a fixed change time.
func statWithCtime() *syscall.Stat_t {
	return &syscall.Stat_t{Ctimespec: syscall.Timespec{Sec: 123456789}}
}
