package system

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCPUPercentFromTicks(t *testing.T) {
	for _, tt := range []struct {
		name          string
		before, after [4]uint32
		want          float64
	}{
		{"idle", [4]uint32{100, 50, 1000, 0}, [4]uint32{100, 50, 1100, 0}, 0},
		{"fully busy", [4]uint32{100, 50, 1000, 0}, [4]uint32{150, 100, 1000, 0}, 100},
		{"half busy", [4]uint32{0, 0, 0, 0}, [4]uint32{25, 25, 50, 0}, 50},
		{"nice counts as busy", [4]uint32{0, 0, 0, 0}, [4]uint32{0, 0, 50, 50}, 50},
		{"counter wraps around", [4]uint32{math.MaxUint32 - 9, 0, 0, 0}, [4]uint32{10, 0, 20, 0}, 50},
	} {
		got, err := cpuPercentFromTicks(tt.before, tt.after)
		require.NoError(t, err, tt.name)
		assert.InDelta(t, tt.want, got, 0.0001, tt.name)
	}
}

func TestCPUPercentFromTicksRejectsASampleWithNoTicks(t *testing.T) {
	same := [4]uint32{1, 2, 3, 4}
	_, err := cpuPercentFromTicks(same, same)
	assert.Error(t, err)
}
