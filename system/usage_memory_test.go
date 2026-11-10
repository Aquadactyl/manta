package system

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryUsedFromFreePercent(t *testing.T) {
	const gib = uint64(1 << 30)

	for _, tt := range []struct {
		total   uint64
		free    uint32
		want    uint64
		message string
	}{
		{16 * gib, 100, 0, "idle memory stays zero"},
		{16 * gib, 75, 4 * gib, "a quarter used"},
		{16 * gib, 50, 8 * gib, "half used"},
		{16 * gib, 0, 16 * gib, "everything used"},
		{1000, 33, 670, "rounds available memory down"},
	} {
		used, err := memoryUsedFromFreePercent(tt.total, tt.free)
		require.NoError(t, err, tt.message)
		assert.Equal(t, tt.want, used, tt.message)
	}
}

func TestMemoryUsedFromFreePercentRejectsInvalidSamples(t *testing.T) {
	for name, tt := range map[string]struct {
		total uint64
		free  uint32
	}{
		"no memory":         {0, 50},
		"more than all":     {1 << 30, 101},
		"would overflow":    {math.MaxUint64, 50},
		"overflow boundary": {math.MaxUint64/100 + 1, 50},
	} {
		_, err := memoryUsedFromFreePercent(tt.total, tt.free)
		assert.Error(t, err, name)
	}
}
