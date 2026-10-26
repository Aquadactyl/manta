package database

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/models"
)

func TestDatabase(t *testing.T) {
	tmp := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			RootDirectory: tmp,
		},
	})

	// Reset state in case previously set
	o.Store(false)
	db = nil

	// Panics when calling Instance before Initialize
	assert.Panics(t, func() {
		Instance()
	})

	// Initialize
	require.NoError(t, Initialize())
	assert.FileExists(t, filepath.Join(tmp, "wings.db"))

	// Instance returns valid DB
	instance := Instance()
	assert.NotNil(t, instance)

	// Can insert and query models.Activity
	act := models.Activity{
		Server: "test-server-1",
		Event:  "command",
		IP:     "127.0.0.1",
	}
	require.NoError(t, instance.Create(&act).Error)
	assert.NotZero(t, act.ID)

	var found models.Activity
	require.NoError(t, instance.First(&found, act.ID).Error)
	assert.Equal(t, "test-server-1", found.Server)

	// Panics if initialized again
	assert.Panics(t, func() {
		_ = Initialize()
	})
}

