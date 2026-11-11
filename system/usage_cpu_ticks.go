package system

import "errors"

// cpuPercentFromTicks returns the CPU utilization between two samples of the
// user, system, idle and nice tick counters, in that order, as the Mach host
// statistics report them. The counters are 32-bit and wrap around, which
// subtracting them as such takes care of.
func cpuPercentFromTicks(before, after [4]uint32) (float64, error) {
	var delta [4]uint64
	for i := range delta {
		delta[i] = uint64(after[i] - before[i])
	}

	total := delta[0] + delta[1] + delta[2] + delta[3]
	if total == 0 {
		return 0, errors.New("CPU sample unavailable")
	}
	return 100 * (1 - float64(delta[2])/float64(total)), nil
}
