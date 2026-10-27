package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/filesystem"
)

type mockBackupRemoteClient struct {
	remote.Client
	urlsResp remote.BackupRemoteUploadResponse
	urlsErr  error
}

func (m *mockBackupRemoteClient) GetBackupRemoteUploadURLs(ctx context.Context, backup string, size int64) (remote.BackupRemoteUploadResponse, error) {
	return m.urlsResp, m.urlsErr
}

func createValidArchiveBytes(t *testing.T) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	data := []byte("test-content")
	hdr := &tar.Header{
		Name: "test.txt",
		Mode: 0644,
		Size: int64(len(data)),
	}
	require.NoError(t, tw.WriteHeader(hdr))
	_, err := tw.Write(data)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

func TestLocateLocalDirectoryError(t *testing.T) {
	backupDir := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			BackupDirectory: backupDir,
		},
	})

	u := uuid.New().String()
	b := NewLocal(nil, u, "")
	// Make b.Path() a directory
	require.NoError(t, os.MkdirAll(b.Path(), 0755))

	_, _, err := LocateLocal(nil, u)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid archive, is directory")
}

func TestRestoreCallbackError(t *testing.T) {
	backupDir := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			BackupDirectory: backupDir,
		},
	})

	u := uuid.New().String()
	b := NewLocal(nil, u, "")
	archiveData := createValidArchiveBytes(t)
	require.NoError(t, os.WriteFile(b.Path(), archiveData, 0644))

	// Local restore callback error
	cbErr := errors.New("callback failed")
	err := b.Restore(context.Background(), nil, func(name string, fi fs.FileInfo, r io.ReadCloser) error {
		return cbErr
	})
	assert.Error(t, err)

	// S3 restore callback error
	s3 := NewS3(nil, u, "")
	err = s3.Restore(context.Background(), bytes.NewReader(archiveData), func(name string, fi fs.FileInfo, r io.ReadCloser) error {
		return cbErr
	})
	assert.Error(t, err)
}

func TestUploadPartErrors(t *testing.T) {
	uploader := newS3FileUploader(io.NopCloser(bytes.NewReader([]byte("part-data"))))

	// 1. Invalid URL scheme
	_, err := uploader.uploadPart(context.Background(), "://invalid-url", 9)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "could not create request for S3")

	// 2. Canceled context
	ctxCanceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = uploader.uploadPart(ctxCanceled, "http://127.0.0.1:9/part", 9)
	assert.Error(t, err)

	// 3. S3 500 error
	var attempts int
	s500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s500.Close()

	ctxTimeout, cancelTimeout := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelTimeout()
	_, err = uploader.uploadPart(ctxTimeout, s500.URL+"/part", 9)
	assert.Error(t, err)
	assert.True(t, attempts > 0)
}

func TestS3BackupMultiPartAndWriteLimit(t *testing.T) {
	backupDir := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			BackupDirectory: backupDir,
			Backups: config.Backups{
				WriteLimit: 5, // 5 MB/s to exercise writeLimit > 0
			},
		},
	})

	partServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"etag-12345"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer partServer.Close()

	u := uuid.New().String()
	mockClient := &mockBackupRemoteClient{
		urlsResp: remote.BackupRemoteUploadResponse{
			Parts:    []string{partServer.URL + "/part1", partServer.URL + "/part2"},
			PartSize: 5,
		},
	}
	s3 := NewS3(mockClient, u, "")

	// Create dummy file for s3.Backup.Size()
	require.NoError(t, os.WriteFile(s3.Path(), []byte("1234567890"), 0644))

	rc, err := os.Open(s3.Path())
	require.NoError(t, err)

	parts, err := s3.generateRemoteRequest(context.Background(), rc)
	require.NoError(t, err)
	assert.Len(t, parts, 2)
	assert.Equal(t, 1, parts[0].PartNumber)
	assert.Equal(t, 2, parts[1].PartNumber)

	// Test Restore with WriteLimit > 0
	archiveData := createValidArchiveBytes(t)
	err = s3.Restore(context.Background(), bytes.NewReader(archiveData), func(name string, fi fs.FileInfo, r io.ReadCloser) error {
		return nil
	})
	require.NoError(t, err)
}

func TestS3BackupGenerateErrors(t *testing.T) {
	backupDir := t.TempDir()
	serverDir := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			BackupDirectory: backupDir,
		},
	})

	fsys, err := filesystem.New(serverDir, 1024*1024, nil)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "test.txt"), []byte("hello"), 0644))

	u := uuid.New().String()
	mockClient := &mockBackupRemoteClient{
		urlsErr: errors.New("failed to get upload URLs"),
	}
	s3 := NewS3(mockClient, u, "")

	// 1. GetBackupRemoteUploadURLs error
	_, err = s3.Generate(context.Background(), fsys, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get upload URLs")

	// 2. Canceled context during Generate
	ctxCancel, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s3.Generate(ctxCancel, fsys, "")
	assert.Error(t, err)

	// 3. LocalBackup Generate with canceled context
	localB := NewLocal(nil, uuid.New().String(), "")
	_, err = localB.Generate(ctxCancel, fsys, "")
	assert.Error(t, err)
}
