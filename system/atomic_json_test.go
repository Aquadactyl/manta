package system

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAtomicJSONRejectsInvalidValuesWithoutChangingState(t *testing.T) {
	b := NewAtomicBool(true)
	s := NewAtomicString("retained")
	n := NewAtomic(42)
	for _, input := range []string{`{`, `"wrong type"`, `[]`} {
		require.Error(t, json.Unmarshal([]byte(input), b))
		require.True(t, b.Load())
		require.Error(t, json.Unmarshal([]byte(input), n))
		require.Equal(t, 42, n.Load())
	}
	for _, input := range []string{`{`, `42`, `[]`} {
		require.Error(t, json.Unmarshal([]byte(input), s))
		require.Equal(t, "retained", s.Load())
	}
}

func TestAtomicZeroValues(t *testing.T) {
	var s AtomicString
	var n Atomic[int]
	require.Empty(t, s.Load())
	require.Zero(t, n.Load())
	data, err := json.Marshal(&n)
	require.NoError(t, err)
	require.JSONEq(t, `0`, string(data))
	require.NoError(t, json.Unmarshal([]byte(`17`), &n))
	require.Equal(t, 17, n.Load())
}
