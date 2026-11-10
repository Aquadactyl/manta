//go:build darwin

package ufs

// toSysMode converts a mode to the type of the mode field of unix.Stat_t.
func toSysMode(m uint32) uint16 { return uint16(m) }
