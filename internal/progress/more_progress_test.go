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
