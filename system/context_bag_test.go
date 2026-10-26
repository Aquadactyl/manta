package system

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContextBag(t *testing.T) {
	parentCtx := context.Background()
	cb := NewContextBag(parentCtx)

	// Context creation and caching
	ctx1 := cb.Context("key1")
	assert.NotNil(t, ctx1)
	assert.NoError(t, ctx1.Err())

	ctx1Same := cb.Context("key1")
	assert.True(t, ctx1 == ctx1Same)

	ctx2 := cb.Context("key2")
	assert.NotNil(t, ctx2)
	assert.False(t, ctx1 == ctx2)

	// Cancel single key
	cb.Cancel("key1")
	assert.ErrorIs(t, ctx1.Err(), context.Canceled)
	assert.NoError(t, ctx2.Err())

	// Cancel non-existent key shouldn't panic
	assert.NotPanics(t, func() {
		cb.Cancel("non-existent")
	})

	// Requesting key1 again creates a fresh context
	ctx1New := cb.Context("key1")
	assert.NotNil(t, ctx1New)
	assert.NoError(t, ctx1New.Err())
	assert.False(t, ctx1 == ctx1New)

	// CancelAll
	ctx3 := cb.Context("key3")
	cb.CancelAll()

	assert.ErrorIs(t, ctx1New.Err(), context.Canceled)
	assert.ErrorIs(t, ctx2.Err(), context.Canceled)
	assert.ErrorIs(t, ctx3.Err(), context.Canceled)

	// Check items map reset
	assert.Empty(t, cb.items)
}
