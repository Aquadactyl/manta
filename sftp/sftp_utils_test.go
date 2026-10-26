package sftp

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/internal/models"
)

type dummyFileInfo struct {
	name string
}

func (d dummyFileInfo) Name() string       { return d.name }
func (d dummyFileInfo) Size() int64        { return 0 }
func (d dummyFileInfo) Mode() os.FileMode  { return 0644 }
func (d dummyFileInfo) ModTime() time.Time { return time.Time{} }
func (d dummyFileInfo) IsDir() bool        { return false }
func (d dummyFileInfo) Sys() any           { return nil }

func TestListerAt(t *testing.T) {
	files := ListerAt{
		dummyFileInfo{name: "f1"},
		dummyFileInfo{name: "f2"},
		dummyFileInfo{name: "f3"},
	}

	// Offset out of range
	buf := make([]os.FileInfo, 2)
	n, err := files.ListAt(buf, 5)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)

	// Partial copy at end of slice
	n, err = files.ListAt(buf, 2)
	assert.Equal(t, 1, n)
	assert.Equal(t, io.EOF, err)
	assert.Equal(t, "f3", buf[0].Name())

	// Exact copy matching buffer size
	n, err = files.ListAt(buf, 0)
	assert.Equal(t, 2, n)
	assert.NoError(t, err)
	assert.Equal(t, "f1", buf[0].Name())
	assert.Equal(t, "f2", buf[1].Name())
}

func TestFxErr(t *testing.T) {
	assert.Equal(t, "Quota Exceeded", ErrSSHQuotaExceeded.Error())
	assert.Equal(t, "Failure", fxErr(99).Error())

	// As tests
	var target int
	assert.False(t, ErrSSHQuotaExceeded.As(target))
	assert.False(t, ErrSSHQuotaExceeded.As(&target))
	assert.False(t, ErrSSHQuotaExceeded.As(nil))
}

func TestPrivateKeyGeneration(t *testing.T) {
	tmp := t.TempDir()
	server := &SFTPServer{
		BasePath: tmp,
	}

	assert.Equal(t, filepath.Join(tmp, ".sftp/id_ed25519"), server.PrivateKeyPath())

	require.NoError(t, server.generateED25519PrivateKey())
	assert.FileExists(t, server.PrivateKeyPath())

	data, err := os.ReadFile(server.PrivateKeyPath())
	require.NoError(t, err)
	assert.Contains(t, string(data), "-----BEGIN PRIVATE KEY-----")
}

func TestEventHandlerLog(t *testing.T) {
	tmp := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			RootDirectory: tmp,
		},
	})
	require.NoError(t, database.Initialize())

	eh := &eventHandler{
		ip:     "127.0.0.1",
		user:   "user-uuid",
		server: "server-uuid",
	}

	// Single entity
	err := eh.Log(models.Event("sftp:file.write"), FileAction{Entity: "/foo.txt"})
	require.NoError(t, err)

	// Rename/Target
	err = eh.Log(models.Event("sftp:file.rename"), FileAction{Entity: "/foo.txt", Target: "/bar.txt"})
	require.NoError(t, err)

	// MustLog
	assert.NotPanics(t, func() {
		eh.MustLog(models.Event("sftp:file.delete"), FileAction{Entity: "/bar.txt"})
	})
}

