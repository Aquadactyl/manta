package sftp

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pterodactyl/wings/internal/models"
)

func TestEventLogAndMustLogHandlePersistenceErrors(t *testing.T) {
	oldPersistActivity := persistActivity
	t.Cleanup(func() { persistActivity = oldPersistActivity })
	persistErr := errors.New("database unavailable")
	persistActivity = func(*models.Activity) error { return persistErr }

	eh := &eventHandler{server: "server", user: "user", ip: "127.0.0.1"}
	err := eh.Log(models.Event("sftp:file.write"), FileAction{Entity: "/file.txt"})
	assert.ErrorIs(t, err, persistErr)
	assert.NotPanics(t, func() {
		eh.MustLog(models.Event("sftp:file.write"), FileAction{Entity: "/file.txt"})
	})
}
