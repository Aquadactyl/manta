//go:build linux

package ufs

// toSysMode converts a mode to the type of the mode field of unix.Stat_t.
func toSysMode(m uint32) uint32 { return m }
