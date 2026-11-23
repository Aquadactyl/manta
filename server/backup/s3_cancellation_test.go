package backup

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Aquadactyl/manta/config"
	"github.com/Aquadactyl/manta/remote"
)

type cancellationBackupClient struct {
	remote.Client
	started chan struct{}
}

func (c cancellationBackupClient) GetBackupRemoteUploadURLs(ctx context.Context, _ string, _ int64) (remote.BackupRemoteUploadResponse, error) {
	close(c.started)
	select {
	case <-ctx.Done():
		return remote.BackupRemoteUploadResponse{}, ctx.Err()
	case <-time.After(2 * time.Second):
		return remote.BackupRemoteUploadResponse{}, context.DeadlineExceeded
	}
}

func TestS3URLRequestStopsWhenBackupIsCancelled(t *testing.T) {
	config.Set(&config.Configuration{System: config.SystemConfiguration{BackupDirectory: t.TempDir()}})
	client := cancellationBackupClient{started: make(chan struct{})}
	backup := NewS3(client, uuid.NewString(), "")
	require.NoError(t, os.WriteFile(backup.Path(), []byte("backup"), 0o600))
	file, err := os.Open(backup.Path())
	require.NoError(t, err)
	defer file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := backup.generateRemoteRequest(ctx, file); done <- err }()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("backup did not request upload URLs")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled backup left the Panel request running")
	}
	_, err = file.Stat()
	require.ErrorIs(t, err, os.ErrClosed)
}
