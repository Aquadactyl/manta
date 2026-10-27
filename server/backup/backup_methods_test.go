package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/filesystem"
)

func TestBackupDetailsAndLocalOperations(t *testing.T) {
	backupDir := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			BackupDirectory: backupDir,
		},
	})

	uuid := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	b := NewLocal(nil, uuid, "ignore-me")
	assert.Equal(t, "ignore-me", b.Ignored())
	assert.Equal(t, uuid, b.Identifier())

	// SetClient
	client := remote.New("http://localhost")
	b.SetClient(client)

	// Create dummy backup file
	require.NoError(t, os.WriteFile(b.Path(), []byte("backup-data-payload"), 0644))

	// Size
	size, err := b.Size()
	require.NoError(t, err)
	assert.Equal(t, int64(19), size)

	// Checksum
	csum, err := b.Checksum()
	require.NoError(t, err)
	assert.NotEmpty(t, csum)

	// Details
	details, err := b.Details(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, int64(19), details.Size)
	assert.NotEmpty(t, details.Checksum)
	assert.Equal(t, "sha1", details.ChecksumType)

	// ToRequest
	req := details.ToRequest(true)
	assert.True(t, req.Successful)
	assert.Equal(t, details.Checksum, req.Checksum)
	assert.Equal(t, int64(19), req.Size)

	// LocateLocal
	locB, fi, err := LocateLocal(nil, uuid)
	require.NoError(t, err)
	assert.Equal(t, uuid, locB.Identifier())
	assert.Equal(t, int64(19), fi.Size())

	// Remove
	require.NoError(t, b.Remove())
	assert.NoFileExists(t, b.Path())

	// LocateLocal after remove returns error
	_, _, err = LocateLocal(nil, uuid)
	assert.Error(t, err)
}

func TestWithLogContext(t *testing.T) {
	uuid := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	localB := NewLocal(nil, uuid, "")
	localB.WithLogContext(map[string]interface{}{"key": "val"})
	assert.NotNil(t, localB.log())

	s3B := NewS3(nil, uuid, "")
	s3B.WithLogContext(map[string]interface{}{"key": "val"})
	assert.NotNil(t, s3B.log())
}

func TestLocalGenerateAndRestore(t *testing.T) {
	root := t.TempDir()
	backupDir := filepath.Join(root, "backups")
	serverDir := filepath.Join(root, "server")
	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.MkdirAll(serverDir, 0755))

	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			BackupDirectory: backupDir,
			Backups: config.Backups{
				WriteLimit: 10,
			},
		},
	})

	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "hello.txt"), []byte("hello world"), 0644))

	fsys, err := filesystem.New(serverDir, 0, nil)
	require.NoError(t, err)

	uuid := "11111111-2222-3333-4444-555555555555"
	b := NewLocal(nil, uuid, "")

	// Test Generate
	details, err := b.Generate(context.Background(), fsys, "")
	require.NoError(t, err)
	assert.True(t, details.Size > 0)
	assert.NotEmpty(t, details.Checksum)
	assert.FileExists(t, b.Path())

	// Test Restore
	restoredFiles := make(map[string]string)
	err = b.Restore(context.Background(), nil, func(name string, fi fs.FileInfo, r io.ReadCloser) error {
		if !fi.IsDir() {
			buf := new(bytes.Buffer)
			_, _ = io.Copy(buf, r)
			restoredFiles[name] = buf.String()
		}
		return nil
	})
	require.NoError(t, err)
	assert.Contains(t, restoredFiles["hello.txt"], "hello world")
}

func TestS3BackupOperations(t *testing.T) {
	root := t.TempDir()
	backupDir := filepath.Join(root, "backups")
	serverDir := filepath.Join(root, "server")
	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.MkdirAll(serverDir, 0755))

	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			BackupDirectory: backupDir,
		},
	})

	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "data.txt"), []byte("s3 backup test"), 0644))

	fsys, err := filesystem.New(serverDir, 0, nil)
	require.NoError(t, err)

	// S3 mock server
	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		w.Header().Set("ETag", "\"tag12345\"")
		w.WriteHeader(http.StatusOK)
	}))
	defer s3Server.Close()

	// Panel mock server
	panelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(remote.BackupRemoteUploadResponse{
			Parts:    []string{s3Server.URL + "/part1"},
			PartSize: 50 * 1024 * 1024,
		})
	}))
	defer panelServer.Close()

	client := remote.New(panelServer.URL, remote.WithCredentials("id", "token"))

	uuid := "22222222-3333-4444-5555-666666666666"
	s3b := NewS3(client, uuid, "")

	// Test Generate
	details, err := s3b.Generate(context.Background(), fsys, "")
	require.NoError(t, err)
	assert.True(t, details.Size > 0)
	assert.Len(t, details.Parts, 1)
	assert.Equal(t, "\"tag12345\"", details.Parts[0].ETag)
	// Local file should be removed
	assert.NoFileExists(t, s3b.Path())

	// Test Restore from stream
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	content := []byte("stream restore data")
	hdr := &tar.Header{
		Name: "restored.txt",
		Mode: 0644,
		Size: int64(len(content)),
	}
	require.NoError(t, tw.WriteHeader(hdr))
	_, err = tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	restoredFiles := make(map[string]string)
	err = s3b.Restore(context.Background(), &buf, func(name string, fi fs.FileInfo, r io.ReadCloser) error {
		if !fi.IsDir() {
			b := new(bytes.Buffer)
			_, _ = io.Copy(b, r)
			restoredFiles[name] = b.String()
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "stream restore data", restoredFiles["restored.txt"])

	// Test S3 Remove
	require.NoError(t, os.WriteFile(s3b.Path(), []byte("temp"), 0644))
	assert.NoError(t, s3b.Remove())
}

func TestS3FileUploaderErrors(t *testing.T) {
	// Reader closer
	r := Reader{Reader: bytes.NewReader([]byte("test"))}
	assert.NoError(t, r.Close())

	// S3 mock returning 400 error
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer errServer.Close()

	uploader := newS3FileUploader(io.NopCloser(bytes.NewReader([]byte("data"))))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := uploader.uploadPart(ctx, errServer.URL+"/part", 4)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestBackupIdentifierValidationAndMissingFileErrors(t *testing.T) {
	// 1. Invalid UUID
	bInvalid := NewLocal(nil, "invalid-uuid", "")
	_, err := bInvalid.Size()
	assert.Error(t, err)
	_, err = bInvalid.Checksum()
	assert.Error(t, err)
	assert.Error(t, bInvalid.Remove())
	_, _, err = LocateLocal(nil, "invalid-uuid")
	assert.Error(t, err)
	// S3 with invalid UUID
	s3Invalid := NewS3(nil, "invalid-uuid", "")
	assert.Error(t, s3Invalid.Remove())

	assert.Error(t, bInvalid.Restore(context.Background(), nil, nil))

	// 2. Valid UUID but file does not exist
	bMissing := NewLocal(nil, "99999999-9999-4999-8999-999999999999", "")
	_, err = bMissing.Size()
	assert.Error(t, err)
	_, err = bMissing.Checksum()
	assert.Error(t, err)
	_, err = bMissing.Details(context.Background(), nil)
	assert.Error(t, err)
	assert.Error(t, bMissing.Restore(context.Background(), nil, nil))
}
