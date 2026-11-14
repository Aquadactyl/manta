package progress

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProgressNegativeTicks(t *testing.T) {
	p := NewProgress(1)
	p.written.Store(math.MaxUint64)
	res := p.Progress(25)
	assert.Contains(t, res, "[                         ]")
}

func TestClampTicksBounds(t *testing.T) {
	assert.Equal(t, 0, clampTicks(-1, 25))
	assert.Equal(t, 25, clampTicks(26, 25))
	assert.Equal(t, 12, clampTicks(12, 25))
}
