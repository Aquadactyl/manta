package cron

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
)

func TestScheduler(t *testing.T) {
	setupDatabase(t)
	defer o.Store(false)

	// Reset state
	o.Store(false)

	// Test invalid timezone
	cfg := config.Get()
	origTz := cfg.System.Timezone
	cfg.System.Timezone = "Invalid/Timezone"
	config.Set(cfg)

	s, err := Scheduler(context.Background(), server.NewEmptyManager(nil))
	assert.Error(t, err)
	assert.Nil(t, s)

	// Reset singleton
	o.Store(false)

	// Test valid timezone
	cfg.System.Timezone = "UTC"
	cfg.System.ActivitySendCount = 100
	cfg.System.ActivitySendInterval = 60
	config.Set(cfg)

	s, err = Scheduler(context.Background(), server.NewEmptyManager(nil))
	assert.NoError(t, err)
	assert.NotNil(t, s)
	defer s.Clear()

	// Calling again should fail
	s2, err2 := Scheduler(context.Background(), server.NewEmptyManager(nil))
	assert.Error(t, err2)
	assert.Nil(t, s2)

	cfg.System.Timezone = origTz
	config.Set(cfg)
}

