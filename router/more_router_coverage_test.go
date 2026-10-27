package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router/downloader"
	"github.com/pterodactyl/wings/router/tokens"
	wserver "github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/transfer"
	"github.com/pterodactyl/wings/system"
)

type customizableEnv struct {
	transferTestEnvironment
	readlogErr     error
	isRunning      bool
	isRunningErr   error
	sendCommandErr error
	destroyErr     error
	waitForStopErr error
}

func (e *customizableEnv) Readlog(n int) ([]string, error) {
	if e.readlogErr != nil {
		return nil, e.readlogErr
	}
	return []string{"ok"}, nil
}

func (e *customizableEnv) IsRunning(context.Context) (bool, error) {
	return e.isRunning, e.isRunningErr
}

func (e *customizableEnv) SendCommand(string) error {
	return e.sendCommandErr
}

func (e *customizableEnv) Destroy() error {
	return e.destroyErr
}

func (e *customizableEnv) WaitForStop(context.Context, time.Duration, bool) error {
	return e.waitForStopErr
}

type failingPanel struct {
	transferTestPanel
	getConfigErr error
}

func (p *failingPanel) GetServerConfiguration(ctx context.Context, u string) (remote.ServerConfigurationResponse, error) {
	if p.getConfigErr != nil {
		return remote.ServerConfigurationResponse{}, p.getConfigErr
	}
	return p.transferTestPanel.GetServerConfiguration(ctx, u)
}

func TestRouterRemainingCoverage(t *testing.T) {
	fp := &failingPanel{}
	h := newTransferHarness(t, &fp.transferTestPanel)
	s := h.seedServer()

	cenv := &customizableEnv{
		transferTestEnvironment: transferTestEnvironment{st: system.NewAtomicString(environment.ProcessOfflineState)},
	}
	s.Environment = cenv

	authReq := func(method, target, body string) *http.Request {
		var r *strings.Reader
		if body != "" {
			r = strings.NewReader(body)
		} else {
			r = strings.NewReader("")
		}
		req := httptest.NewRequest(method, target, r)
		req.Header.Set("Authorization", "Bearer test-token")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		return req
	}

	// 1. getServerLogs clamp and error
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodGet, "/api/servers/"+s.ID()+"/logs?size=-5", ""))
	assert.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodGet, "/api/servers/"+s.ID()+"/logs?size=500", ""))
	assert.Equal(t, http.StatusOK, w.Code)

	cenv.readlogErr = errors.New("readlog boom")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodGet, "/api/servers/"+s.ID()+"/logs", ""))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	cenv.readlogErr = nil

	// 2. postServerPower branches
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/power", "{invalid"))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/power", `{"action":"invalid"}`))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	s.Config().SetSuspended(true)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/power", `{"action":"start"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	s.Config().SetSuspended(false)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/power", `{"action":"stop","wait_seconds":500}`))
	assert.Equal(t, http.StatusAccepted, w.Code)

	// 3. postServerCommands branches
	cenv.isRunningErr = errors.New("isrunning boom")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/commands", `{"commands":["say hi"]}`))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	cenv.isRunningErr = nil

	cenv.isRunning = false
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/commands", `{"commands":["say hi"]}`))
	assert.Equal(t, http.StatusBadGateway, w.Code)

	cenv.isRunning = true
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/commands", "{invalid"))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	cenv.sendCommandErr = errors.New("sendcmd boom")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/commands", `{"commands":["say hi"]}`))
	assert.Equal(t, http.StatusNoContent, w.Code)
	cenv.sendCommandErr = nil
	cenv.isRunning = false

	// 4. postServerTransfer branches
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", "{invalid"))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", `{"url":"ftp://bad","token":"tok"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	s.SetTransferring(true)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", `{"url":"https://example.com/api/transfers","token":"tok"}`))
	assert.Equal(t, http.StatusConflict, w.Code)

	// deleteServerTransfer when IsTransferring is true but Outgoing().Get is nil
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodDelete, "/api/servers/"+s.ID()+"/transfer", ""))
	assert.Equal(t, http.StatusConflict, w.Code)
	s.SetTransferring(false)

	// postServerTransfer when WaitForStop fails
	cenv.SetState(environment.ProcessRunningState)
	cenv.waitForStopErr = errors.New("stop timeout")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", `{"url":"https://example.com/api/transfers","token":"tok"}`))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	cenv.waitForStopErr = nil
	cenv.SetState(environment.ProcessOfflineState)

	// postServerTransfer when Outgoing().Add returns false
	existingTr := transfer.New(context.Background(), s)
	transfer.Outgoing().Add(existingTr)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/transfer", `{"url":"https://example.com/api/transfers","token":"tok"}`))
	assert.Equal(t, http.StatusConflict, w.Code)
	transfer.Outgoing().Remove(existingTr)

	// 5. router_server_files.go branches
	// postServerWriteFile missing Content-Length
	reqWrite := authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=/test.txt", "data")
	reqWrite.ContentLength = -1
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, reqWrite)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// postServerWriteFile on existing directory
	require.NoError(t, s.Filesystem().CreateDirectory("conflict_dir", "/"))
	reqWriteDir := authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/write?file=/conflict_dir", "data")
	reqWriteDir.ContentLength = 4
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, reqWriteDir)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// postServerCopyFile invalid JSON & missing file
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/copy", "{invalid"))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/copy", `{"location":"/missing_copy.txt"}`))
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	// postServerCompressFiles invalid JSON, empty files, bad root path
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/compress", "{invalid"))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/compress", `{"root":"/","files":[]}`))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/compress", `{"root":"../../outside","files":["a.txt"]}`))
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	// postServerPullRemoteFile branches: deprecated directory field, url.Parse error, HasSpaceErr
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/pull", `{"directory":"/","url":"http://[::1]:badport"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	s.Filesystem().SetDiskLimit(-1)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/pull", `{"directory":"/","url":"https://example.com/file.txt"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	s.Filesystem().SetDiskLimit(0)

	// postUpdateConfiguration & postDeauthorizeUser error branches
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/update", "{invalid"))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/deauthorize-user", "{invalid"))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// postServerDecompressFiles invalid JSON
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/decompress", "{invalid"))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// postServerChmodFile invalid JSON, empty files, invalid octal mode, non-existent file
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", "{invalid"))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", `{"root":"/","files":[]}`))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", `{"root":"/","files":[{"file":"a.txt","mode":"not-octal"}]}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/files/chmod", `{"root":"/","files":[{"file":"nonexistent_chmod.txt","mode":"0644"}]}`))
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 6. getDownloadBackup & getDownloadFile branches
	now := time.Now().Add(2 * time.Second)
	badBackupTok := tokens.BackupPayload{
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(10 * time.Minute)),
		},
		Scoped:     tokens.Scoped{Scope: string(tokens.BackupDownload)},
		ServerUuid: s.ID(),
		UserUuid:   "user-1",
		BackupUuid: "not-a-valid-uuid",
		UniqueId:   uuid.New().String(),
	}
	signedBadBackup, err := jwt.Sign(&badBackupTok, config.GetJwtAlgorithm())
	require.NoError(t, err)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/download/backup?token="+string(signedBadBackup), nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	missingBackupTok := badBackupTok
	missingBackupTok.BackupUuid = uuid.New().String()
	missingBackupTok.UniqueId = uuid.New().String()
	signedMissingBackup, err := jwt.Sign(&missingBackupTok, config.GetJwtAlgorithm())
	require.NoError(t, err)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/download/backup?token="+string(signedMissingBackup), nil))
	assert.Equal(t, http.StatusNotFound, w.Code)

	dirFileTok := tokens.FilePayload{
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			NotBefore:      jwt.NumericDate(now.Add(-5 * time.Second)),
			ExpirationTime: jwt.NumericDate(now.Add(10 * time.Minute)),
		},
		Scoped:     tokens.Scoped{Scope: string(tokens.FileDownload)},
		ServerUuid: s.ID(),
		UserUuid:   "user-1",
		FilePath:   "/conflict_dir",
		UniqueId:   uuid.New().String(),
	}
	signedDirFile, err := jwt.Sign(&dirFileTok, config.GetJwtAlgorithm())
	require.NoError(t, err)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/download/file?token="+string(signedDirFile), nil))
	assert.Equal(t, http.StatusNotFound, w.Code)

	// 7. deleteServerBackup when backup exists on disk
	backupUUID := uuid.New().String()
	backupDir := filepath.Join(config.Get().System.BackupDirectory, s.ID())
	require.NoError(t, os.MkdirAll(backupDir, 0o755))
	backupFile := filepath.Join(backupDir, backupUUID+".tar.gz")
	require.NoError(t, os.WriteFile(backupFile, []byte("backup-bytes"), 0o644))
	// LocateLocal looks in config.Get().System.BackupDirectory/<uuid>.tar.gz
	flatBackupFile := filepath.Join(config.Get().System.BackupDirectory, backupUUID+".tar.gz")
	require.NoError(t, os.WriteFile(flatBackupFile, []byte("backup-bytes"), 0o644))

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodDelete, "/api/servers/"+s.ID()+"/backup/"+backupUUID, ""))
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 8. validateBackupDownloadUrl & backupRestoreHttpClient
	assert.Error(t, validateBackupDownloadUrl("http:///no-host"))
	assert.Error(t, validateBackupDownloadUrl("ftp://example.com/backup.tar.gz"))
	assert.Error(t, validateBackupDownloadUrl("http://127.0.0.1/backup.tar.gz"))

	bClient := backupRestoreHttpClient()
	via := make([]*http.Request, 10)
	reqRedir, _ := http.NewRequest(http.MethodGet, "https://example.com/b.tar.gz", nil)
	assert.Error(t, bClient.CheckRedirect(reqRedir, via))
	reqBadRedir, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/b.tar.gz", nil)
	assert.Error(t, bClient.CheckRedirect(reqBadRedir, via[:1]))

	tr := bClient.Transport.(*http.Transport)
	_, err = tr.DialContext(context.Background(), "tcp", "invalid-host-port")
	assert.Error(t, err)
	_, err = tr.DialContext(context.Background(), "tcp", "127.0.0.1:80")
	assert.Error(t, err)

	// 9. postCreateServer branches (invalid JSON, GetServerConfiguration error, and success with skip_egg_scripts)
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers", "{invalid"))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	existingContainerUUID := "82595161-eed8-4536-8f50-689b2c2912bb"
	rawSettings, _ := json.Marshal(map[string]any{
		"uuid":             existingContainerUUID,
		"skip_egg_scripts": true,
		"build":            map[string]any{"memory_limit": 128, "disk_space": 128},
		"container":        map[string]any{"image": "~alpine:latest"},
	})
	h.panel.config = remote.ServerConfigurationResponse{
		Settings:             rawSettings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	}
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers", `{"uuid":"`+existingContainerUUID+`","start_on_completion":false}`))
	assert.Equal(t, http.StatusAccepted, w.Code)
	time.Sleep(40 * time.Millisecond)

	h.mgr.Remove(func(srv *wserver.Server) bool { return srv.ID() == existingContainerUUID })
	rawSettingsSuspended, _ := json.Marshal(map[string]any{
		"uuid":             existingContainerUUID,
		"suspended":        true,
		"skip_egg_scripts": true,
		"build":            map[string]any{"memory_limit": 128, "disk_space": 128},
		"container":        map[string]any{"image": "~alpine:latest"},
	})
	h.panel.config = remote.ServerConfigurationResponse{
		Settings:             rawSettingsSuspended,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	}
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers", `{"uuid":"`+existingContainerUUID+`","start_on_completion":true}`))
	assert.Equal(t, http.StatusAccepted, w.Code)
	time.Sleep(40 * time.Millisecond)
	h.mgr.Remove(func(srv *wserver.Server) bool { return srv.ID() == existingContainerUUID })

	// 9b. postServerSync & postServerInstall & postServerReinstall
	sSettings, _ := json.Marshal(map[string]any{"uuid": s.ID(), "skip_egg_scripts": true})
	h.panel.config = remote.ServerConfigurationResponse{
		Settings:             sSettings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	}
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/sync", ""))
	assert.Equal(t, http.StatusNoContent, w.Code)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/install", ""))
	assert.Equal(t, http.StatusAccepted, w.Code)

	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodPost, "/api/servers/"+s.ID()+"/reinstall", ""))
	assert.Equal(t, http.StatusAccepted, w.Code)
	time.Sleep(30 * time.Millisecond)

	// 10. deleteServer when IsTransferring and Environment.Destroy fails
	s.SetTransferring(true)
	uDl, _ := url.Parse("https://example.com/a.txt")
	dl := downloader.New(s, downloader.DownloadRequest{Directory: "/", URL: uDl, FileName: "a.txt"})
	defer dl.Cancel()
	cenv.destroyErr = errors.New("destroy boom")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, authReq(http.MethodDelete, "/api/servers/"+s.ID(), ""))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	cenv.destroyErr = nil
}
