package database

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

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

	// Error path: unwritable path
	o.Store(false)
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			RootDirectory: "/dev/null/impossible-path",
		},
	})
	assert.Error(t, Initialize())

	// Error path: AutoMigrate error due to conflicting view
	migDir := t.TempDir()
	dbPath := filepath.Join(migDir, "wings.db")
	sdb, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	_, err = sdb.Exec("CREATE VIEW activities AS SELECT 1;")
	require.NoError(t, err)
	_ = sdb.Close()

	o.Store(false)
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			RootDirectory: migDir,
		},
	})
	assert.Error(t, Initialize())
}

func TestInitializeReportsDatabaseSetupErrors(t *testing.T) {
	oldDatabaseSQL, oldDatabaseExec := databaseSQL, databaseExec
	t.Cleanup(func() {
		databaseSQL = oldDatabaseSQL
		databaseExec = oldDatabaseExec
	})

	for _, test := range []struct {
		name       string
		failAtExec int
		failSQLDB  bool
	}{
		{name: "obtain sql connection", failSQLDB: true},
		{name: "set synchronous pragma", failAtExec: 1},
		{name: "set journal pragma", failAtExec: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			config.Set(&config.Configuration{System: config.SystemConfiguration{RootDirectory: root}})
			o.Store(false)
			want := errors.New("injected database setup error")
			databaseSQL = oldDatabaseSQL
			databaseExec = oldDatabaseExec
			if test.failSQLDB {
				databaseSQL = func(*gorm.DB) (*sql.DB, error) { return nil, want }
			}
			if test.failAtExec != 0 {
				calls := 0
				databaseExec = func(db *gorm.DB, query string) error {
					calls++
					if calls == test.failAtExec {
						return want
					}
					return oldDatabaseExec(db, query)
				}
			}
			require.ErrorIs(t, Initialize(), want)
		})
	}
}
