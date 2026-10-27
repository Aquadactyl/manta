package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"emperror.dev/errors"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteErrors(t *testing.T) {
	assert.False(t, IsRequestError(nil))
	assert.False(t, IsRequestError(errors.New("generic error")))

	rerr := &RequestError{
		Code:   "BadCode",
		Status: "400",
		Detail: "Something broke",
		response: &http.Response{
			StatusCode: http.StatusBadRequest,
		},
	}

	assert.True(t, IsRequestError(rerr))
	assert.True(t, IsRequestError(errors.Wrap(rerr, "wrapped")))
	assert.Nil(t, AsRequestError(nil))
	assert.Nil(t, AsRequestError(errors.New("other")))
	assert.Equal(t, rerr, AsRequestError(rerr))
	assert.Equal(t, rerr, AsRequestError(errors.Wrap(rerr, "wrapped")))
	assert.Equal(t, 400, rerr.StatusCode())
	assert.Contains(t, rerr.Error(), "BadCode: Something broke (HTTP/400)")

	rerrNoResp := &RequestError{Code: "C", Detail: "D"}
	assert.Contains(t, rerrNoResp.Error(), "(HTTP/0)")

	sftpErr := SftpInvalidCredentialsError{}
	assert.Equal(t, "the credentials provided were invalid", sftpErr.Error())
}

func TestResetServersState(t *testing.T) {
	called := false
	c, s := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/servers/reset", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		called = true
		w.WriteHeader(http.StatusOK)
	})
	defer s.Close()

	err := c.ResetServersState(context.Background())
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestGetServerConfiguration(t *testing.T) {
	c, s := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/servers/123", r.URL.Path)
		_ = json.NewEncoder(w).Encode(ServerConfigurationResponse{
			Settings: json.RawMessage(`{"uuid":"123"}`),
			ProcessConfiguration: &ProcessConfiguration{
				Startup: struct {
					Done            []*OutputLineMatcher `json:"done"`
					UserInteraction []string             `json:"user_interaction"`
					StripAnsi       bool                 `json:"strip_ansi"`
				}{
					UserInteraction: []string{"Ready"},
				},
			},
		})
	})
	defer s.Close()

	cfg, err := c.GetServerConfiguration(context.Background(), "123")
	assert.NoError(t, err)
	assert.NotNil(t, cfg.ProcessConfiguration)
	assert.Equal(t, []string{"Ready"}, cfg.ProcessConfiguration.Startup.UserInteraction)
}

func TestGetInstallationScript(t *testing.T) {
	c, s := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/servers/123/install", r.URL.Path)
		_ = json.NewEncoder(w).Encode(InstallationScript{
			ContainerImage: "alpine:latest",
			Entrypoint:     "ash",
			Script:         "echo hello",
		})
	})
	defer s.Close()

	script, err := c.GetInstallationScript(context.Background(), "123")
	assert.NoError(t, err)
	assert.Equal(t, "alpine:latest", script.ContainerImage)
	assert.Equal(t, "ash", script.Entrypoint)
	assert.Equal(t, "echo hello", script.Script)
}

func TestSetInstallationStatus(t *testing.T) {
	called := false
	c, s := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/servers/123/install", r.URL.Path)
		called = true
		w.WriteHeader(http.StatusOK)
	})
	defer s.Close()

	err := c.SetInstallationStatus(context.Background(), "123", InstallStatusRequest{Successful: true})
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestSetArchiveStatus(t *testing.T) {
	called := false
	c, s := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/servers/123/archive", r.URL.Path)
		called = true
		w.WriteHeader(http.StatusOK)
	})
	defer s.Close()

	err := c.SetArchiveStatus(context.Background(), "123", true)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestSetTransferStatus(t *testing.T) {
	paths := []string{}
	c, s := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})
	defer s.Close()

	err := c.SetTransferStatus(context.Background(), "123", true)
	assert.NoError(t, err)
	err = c.SetTransferStatus(context.Background(), "123", false)
	assert.NoError(t, err)

	assert.Contains(t, paths, "/servers/123/transfer/success")
	assert.Contains(t, paths, "/servers/123/transfer/failure")
}

func TestValidateSftpCredentials(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		c, s := createTestClient(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/sftp/auth", r.URL.Path)
			_ = json.NewEncoder(w).Encode(SftpAuthResponse{
				Server:      "server-uuid",
				Permissions: []string{"*"},
			})
		})
		defer s.Close()

		res, err := c.ValidateSftpCredentials(context.Background(), SftpAuthRequest{
			User: "user",
			Pass: "pass",
			IP:   "127.0.0.1",
		})
		assert.NoError(t, err)
		assert.Equal(t, "server-uuid", res.Server)
	})

	t.Run("invalid credentials 403", func(t *testing.T) {
		c, s := createTestClient(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errors": []map[string]any{
					{
						"code":   "InvalidCredentials",
						"status": "403",
						"detail": "Bad credentials",
					},
				},
			})
		})
		defer s.Close()

		_, err := c.ValidateSftpCredentials(context.Background(), SftpAuthRequest{
			User: "bad",
			Pass: "credentials",
		})
		assert.Error(t, err)
		assert.IsType(t, &SftpInvalidCredentialsError{}, err)
	})
}

func TestBackupMethods(t *testing.T) {
	c, s := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backups/b1":
			if r.Method == http.MethodGet {
				assert.Equal(t, "1024", r.URL.Query().Get("size"))
				_ = json.NewEncoder(w).Encode(BackupRemoteUploadResponse{
					Parts: []string{"http://s3.part1"},
				})
			} else {
				w.WriteHeader(http.StatusOK)
			}
		case "/backups/b1/restore":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	})
	defer s.Close()

	urls, err := c.GetBackupRemoteUploadURLs(context.Background(), "b1", 1024)
	assert.NoError(t, err)
	assert.Equal(t, []string{"http://s3.part1"}, urls.Parts)

	err = c.SetBackupStatus(context.Background(), "b1", BackupRequest{Successful: true})
	assert.NoError(t, err)

	err = c.SendRestorationStatus(context.Background(), "b1", true)
	assert.NoError(t, err)
}

func TestSendActivityLogs(t *testing.T) {
	called := false
	c, s := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/activity", r.URL.Path)
		called = true
		w.WriteHeader(http.StatusOK)
	})
	defer s.Close()

	err := c.SendActivityLogs(context.Background(), []models.Activity{
		{Event: "server:start"},
	})
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestGetServers_SinglePage(t *testing.T) {
	c, s := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []RawServerData{
				{Uuid: "s1"},
				{Uuid: "s2"},
			},
			"meta": Pagination{
				CurrentPage: 1,
				LastPage:    1,
			},
		})
	})
	defer s.Close()

	srvs, err := c.GetServers(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, srvs, 2)
}

func TestRemoteClientContextCanceledErrors(t *testing.T) {
	c := New("http://127.0.0.1:12345")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	assert.Error(t, c.ResetServersState(ctx))
	_, err := c.GetServerConfiguration(ctx, "s1")
	assert.Error(t, err)
	_, err = c.GetInstallationScript(ctx, "s1")
	assert.Error(t, err)
	assert.Error(t, c.SetInstallationStatus(ctx, "s1", InstallStatusRequest{Successful: true}))
	assert.Error(t, c.SetArchiveStatus(ctx, "s1", true))
	assert.Error(t, c.SetTransferStatus(ctx, "s1", true))
	_, err = c.ValidateSftpCredentials(ctx, SftpAuthRequest{})
	assert.Error(t, err)
	_, err = c.GetBackupRemoteUploadURLs(ctx, "s1", 100)
	assert.Error(t, err)
	assert.Error(t, c.SetBackupStatus(ctx, "s1", BackupRequest{}))
	assert.Error(t, c.SendRestorationStatus(ctx, "s1", true))
	assert.Error(t, c.SendActivityLogs(ctx, nil))
	_, err = c.GetServers(ctx, 10)
	assert.Error(t, err)
}
