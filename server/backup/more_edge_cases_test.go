package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mholt/archives"
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
	onURLs   func()
}

func (m *mockBackupRemoteClient) GetBackupRemoteUploadURLs(ctx context.Context, backup string, size int64) (remote.BackupRemoteUploadResponse, error) {
	if m.onURLs != nil {
		m.onURLs()
	}
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
	previousOpen := openArchiveEntry
	openArchiveEntry = func(archives.FileInfo) (io.ReadCloser, error) { return nil, errors.New("entry open failed") }
	t.Cleanup(func() { openArchiveEntry = previousOpen })
	err = s3.Restore(context.Background(), bytes.NewReader(archiveData), func(string, fs.FileInfo, io.ReadCloser) error { return nil })
	assert.ErrorContains(t, err, "entry open failed")
	localOpen := NewLocal(nil, u, "")
	require.NoError(t, os.WriteFile(localOpen.Path(), archiveData, 0644))
	err = localOpen.Restore(context.Background(), nil, func(string, fs.FileInfo, io.ReadCloser) error { return nil })
	assert.ErrorContains(t, err, "entry open failed")
}

func TestBackupChecksumCopyError(t *testing.T) {
	backupDir := t.TempDir()
	config.Set(&config.Configuration{System: config.SystemConfiguration{BackupDirectory: backupDir}})
	b := NewLocal(nil, uuid.New().String(), "")
	require.NoError(t, os.WriteFile(b.Path(), []byte("data"), 0644))
	previous := copyChecksum
	copyChecksum = func(io.Writer, io.Reader, []byte) (int64, error) { return 0, errors.New("checksum read failed") }
	t.Cleanup(func() { copyChecksum = previous })
	_, err := b.Checksum()
	assert.ErrorContains(t, err, "checksum read failed")
}

func TestLocalGenerateDetailsError(t *testing.T) {
	root := t.TempDir()
	backupDir := filepath.Join(root, "backups")
	serverDir := filepath.Join(root, "server")
	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.MkdirAll(serverDir, 0755))
	config.Set(&config.Configuration{System: config.SystemConfiguration{BackupDirectory: backupDir}})
	fsys, err := filesystem.New(serverDir, 0, nil)
	require.NoError(t, err)
	previous := localBackupDetails
	localBackupDetails = func(*Backup, context.Context) (*ArchiveDetails, error) { return nil, errors.New("details failed") }
	t.Cleanup(func() { localBackupDetails = previous })
	b := NewLocal(nil, uuid.New().String(), "")
	_, err = b.Generate(context.Background(), fsys, "")
	assert.ErrorContains(t, err, "details failed")
}

func TestUploadPartErrors(t *testing.T) {
	uploader := newS3FileUploader(bytes.NewReader([]byte("part-data")))

	// 1. Invalid URL scheme
	_, err := uploader.uploadPart(context.Background(), "://invalid-url", 0, 9)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "could not create request for S3")

	// 2. Canceled context
	ctxCanceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = uploader.uploadPart(ctxCanceled, "http://127.0.0.1:9/part", 0, 9)
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
	_, err = uploader.uploadPart(ctxTimeout, s500.URL+"/part", 0, 9)
	assert.Error(t, err)
	assert.True(t, attempts > 0)

	// A refused connection is retryable; the request error is wrapped before
	// the context ends the retry loop.
	closedListener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, listenErr)
	refusedURL := "http://" + closedListener.Addr().String() + "/part"
	require.NoError(t, closedListener.Close())
	retryCtx, cancelRetry := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelRetry()
	_, err = uploader.uploadPart(retryCtx, refusedURL, 0, 9)
	assert.Error(t, err)
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

	// 4. Part upload failure is returned by the S3 generation path.
	badPartClient := &mockBackupRemoteClient{urlsResp: remote.BackupRemoteUploadResponse{
		Parts: []string{"://invalid-upload-url"},
	}}
	badPartBackup := NewS3(badPartClient, uuid.New().String(), "")
	_, err = badPartBackup.Generate(context.Background(), fsys, "")
	assert.ErrorContains(t, err, "could not create request for S3")
}

func TestS3BackupGenerateArchiveAndDetailsFailures(t *testing.T) {
	backupDir := t.TempDir()
	serverDir := t.TempDir()
	config.Set(&config.Configuration{AuthenticationToken: "test-token", System: config.SystemConfiguration{BackupDirectory: backupDir}})
	fsys, err := filesystem.New(serverDir, 1024*1024, nil)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "test.txt"), []byte("hello"), 0644))

	t.Run("archive not present after creation", func(t *testing.T) {
		previousCreate := createS3Archive
		createS3Archive = func(context.Context, *filesystem.Archive, string) error { return nil }
		t.Cleanup(func() { createS3Archive = previousCreate })
		b := NewS3(&mockBackupRemoteClient{}, uuid.New().String(), "")
		_, err := b.Generate(context.Background(), fsys, "")
		require.ErrorContains(t, err, "could not read archive from disk")
	})

	t.Run("archive disappears before details are collected", func(t *testing.T) {
		b := NewS3(nil, uuid.New().String(), "")
		client := &mockBackupRemoteClient{onURLs: func() { _ = os.Remove(b.Path()) }}
		b.client = client
		_, err := b.Generate(context.Background(), fsys, "")
		require.ErrorContains(t, err, "failed to get archive details after upload")
	})

	t.Run("temporary archive cleanup error is logged", func(t *testing.T) {
		previousRemove := removeS3BackupFile
		removeS3BackupFile = func(string) error { return errors.New("cleanup failed") }
		t.Cleanup(func() { removeS3BackupFile = previousRemove })
		b := NewS3(&mockBackupRemoteClient{urlsErr: errors.New("upload URL failed")}, uuid.New().String(), "")
		_, err := b.Generate(context.Background(), fsys, "")
		require.ErrorContains(t, err, "upload URL failed")
		_ = os.Remove(b.Path())
	})
}

func TestS3GenerateRemoteRequestReturnsSizeError(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token", System: config.SystemConfiguration{BackupDirectory: t.TempDir()}})
	file, err := os.CreateTemp(t.TempDir(), "backup-input")
	require.NoError(t, err)
	s3 := NewS3(&mockBackupRemoteClient{}, uuid.New().String(), "")
	_, err = s3.generateRemoteRequest(context.Background(), file)
	require.Error(t, err)
	require.ErrorContains(t, err, "no such file")
}
