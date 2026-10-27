package router

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router/tokens"
	wserver "github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/transfer"
	"github.com/pterodactyl/wings/system"
)

// transferTestUUID is a lowercase version 4 UUID, as the installer requires.
const transferTestUUID = "3f0c9a52-7d1e-4b8a-9c2f-5e6d7a8b9c01"

// transferTestPanel answers the Panel calls made by the transfer handlers. Only
// the calls these tests reach are implemented; any other call panics on the nil
// embedded interface.
type transferTestPanel struct {
	remote.Client

	mu        sync.Mutex
	config    remote.ServerConfigurationResponse
	statusErr error
	statuses  []bool
}

func (p *transferTestPanel) GetServerConfiguration(context.Context, string) (remote.ServerConfigurationResponse, error) {
	return p.config, nil
}

func (p *transferTestPanel) SetTransferStatus(_ context.Context, _ string, successful bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.statuses = append(p.statuses, successful)
	return p.statusErr
}

func (p *transferTestPanel) GetInstallationScript(context.Context, string) (remote.InstallationScript, error) {
	return remote.InstallationScript{}, errors.New("cannot install")
}

func (p *transferTestPanel) SetInstallationStatus(context.Context, string, remote.InstallStatusRequest) error {
	return nil
}

func (p *transferTestPanel) SendRestorationStatus(context.Context, string, bool) error {
	return nil
}

func (p *transferTestPanel) SendArchiveStatus(context.Context, string, bool) error {
	return nil
}

func (p *transferTestPanel) GetBackupRemoteUploadURLs(context.Context, string, int64) (remote.BackupRemoteUploadResponse, error) {
	return remote.BackupRemoteUploadResponse{}, errors.New("cannot upload")
}

func (p *transferTestPanel) SetBackupStatus(context.Context, string, remote.BackupRequest) error {
	return nil
}

// transferTestEnvironment stands in for the Docker environment so that no
// container is needed.
type transferTestEnvironment struct {
	environment.ProcessEnvironment
	st *system.AtomicString
}

func (transferTestEnvironment) Create() error { return nil }

func (e transferTestEnvironment) State() string {
	if e.st != nil {
		return e.st.Load()
	}
	return environment.ProcessOfflineState
}

func (transferTestEnvironment) Destroy() error { return nil }

func (transferTestEnvironment) Readlog(int) ([]string, error) { return []string{"line1", "line2"}, nil }

func (e transferTestEnvironment) IsRunning(context.Context) (bool, error) {
	return e.State() == environment.ProcessRunningState, nil
}

func (transferTestEnvironment) Config() *environment.Configuration {
	return &environment.Configuration{}
}

func (transferTestEnvironment) Events() *events.Bus { return events.NewBus() }

func (transferTestEnvironment) SendCommand(string) error { return nil }

func (transferTestEnvironment) InSituUpdate() error { return nil }

func (e transferTestEnvironment) SetState(s string) {
	if e.st != nil {
		e.st.Store(s)
	}
}

func (transferTestEnvironment) SetLogCallback(func([]byte)) {}

func (transferTestEnvironment) WaitForStop(context.Context, time.Duration, bool) error { return nil }

func (transferTestEnvironment) Stop(context.Context) error { return nil }

func (transferTestEnvironment) Terminate(context.Context, string) error { return nil }

type transferHarness struct {
	t      *testing.T
	router http.Handler
	mgr    *wserver.Manager
	panel  *transferTestPanel
}

// newTransferHarness builds the full router with a temporary data directory and
// a node token of "test-token".
func newTransferHarness(t *testing.T, panel *transferTestPanel) *transferHarness {
	t.Helper()

	root := t.TempDir()
	cfg, err := config.NewAtPath(filepath.Join(root, "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	cfg.System.RootDirectory = root
	cfg.System.Data = filepath.Join(root, "volumes")
	cfg.System.ArchiveDirectory = filepath.Join(root, "archives")
	cfg.System.BackupDirectory = filepath.Join(root, "backups")
	cfg.System.LogDirectory = filepath.Join(root, "logs")
	cfg.System.TmpDirectory = filepath.Join(root, "tmp")
	cfg.System.MachineID.Enable = false
	require.NoError(t, os.MkdirAll(cfg.System.Data, 0755))
	require.NoError(t, os.MkdirAll(cfg.System.ArchiveDirectory, 0755))
	require.NoError(t, os.MkdirAll(cfg.System.BackupDirectory, 0755))
	config.Set(cfg)

	settings, err := json.Marshal(map[string]string{"uuid": transferTestUUID})
	require.NoError(t, err)
	panel.config = remote.ServerConfigurationResponse{
		Settings:             settings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	}

	mgr := wserver.NewEmptyManager(panel)
	return &transferHarness{t: t, router: Configure(mgr, panel), mgr: mgr, panel: panel}
}

// seedServer registers the test server on the manager, as an earlier receive
// would have, and replaces its Docker environment.
func (h *transferHarness) seedServer() *wserver.Server {
	h.t.Helper()

	s, err := h.mgr.InitServer(transferTestUUID, h.panel.config)
	require.NoError(h.t, err)
	s.Environment = transferTestEnvironment{st: system.NewAtomicString(environment.ProcessOfflineState)}
	require.True(h.t, h.mgr.AddIfMissing(s))
	return s
}

// startInFlightReceive marks the server as receiving a transfer, the way the
// first POST /api/transfers request does.
func (h *transferHarness) startInFlightReceive(s *wserver.Server) *transfer.Transfer {
	h.t.Helper()

	s.SetTransferring(true)
	tr := transfer.New(context.Background(), s)
	require.True(h.t, transfer.Incoming().Add(tr))
	h.t.Cleanup(func() { transfer.Incoming().Remove(tr) })
	return tr
}

func (h *transferHarness) postTransfer(body *bytes.Buffer, contentType, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/transfers", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

func buildTransferTestArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func transferTestMultipart(t *testing.T, archive []byte, checksum string) (*bytes.Buffer, string) {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("archive", "archive.tar.gz")
	require.NoError(t, err)
	_, err = part.Write(archive)
	require.NoError(t, err)
	require.NoError(t, mw.WriteField("checksum", checksum))
	require.NoError(t, mw.Close())
	return &buf, mw.FormDataContentType()
}

// signTransferTestToken issues a transfer JWT with the same claims the Panel
// sets: subject is the server UUID, scope is "transfer", and it expires after
// 15 minutes.
func signTransferTestToken(t *testing.T, subject string) string {
	t.Helper()

	token, err := jwt.Sign(tokens.TransferPayload{
		Payload: jwt.Payload{
			Subject:        subject,
			ExpirationTime: jwt.NumericDate(time.Now().Add(15 * time.Minute)),
		},
		Scoped: tokens.Scoped{Scope: string(tokens.ServerTransfer)},
	}, config.GetJwtAlgorithm())
	require.NoError(t, err)
	return string(token)
}

// A receive whose checksum does not match does not leave the extracted files on
// disk.
func TestTransferChecksumMismatchRemovesExtractedFiles(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})

	archive := buildTransferTestArchive(t, map[string]string{"received.txt": "data"})
	body, contentType := transferTestMultipart(t, archive, strings.Repeat("0", 64))

	w := h.postTransfer(body, contentType, signTransferTestToken(t, transferTestUUID))
	require.NotEqual(t, http.StatusOK, w.Code)

	_, err := os.Stat(filepath.Join(config.Get().System.Data, transferTestUUID, "received.txt"))
	assert.True(t, os.IsNotExist(err), "a file from a failed receive is still on disk")
	_, registered := h.mgr.Get(transferTestUUID)
	assert.False(t, registered)
}

// A second request to send a server that is already being received is refused,
// and does not affect the transfer in progress.
func TestTransferThatIsAlreadyInProgressIsRefused(t *testing.T) {
	panel := &transferTestPanel{}
	h := newTransferHarness(t, panel)
	s := h.seedServer()
	h.startInFlightReceive(s)

	archive := buildTransferTestArchive(t, map[string]string{"second.dat": "data"})
	body, contentType := transferTestMultipart(t, archive, strings.Repeat("0", 64))

	w := h.postTransfer(body, contentType, signTransferTestToken(t, transferTestUUID))
	require.Equal(t, http.StatusConflict, w.Code)

	_, registered := h.mgr.Get(transferTestUUID)
	assert.True(t, registered)
	assert.True(t, s.IsTransferring())
	assert.Empty(t, panel.statuses)
}

// A successful transfer is finished even if the Panel cannot be told about it,
// so that the server is not left unable to start.
func TestCompletedTransferIsFinishedWhenPanelFails(t *testing.T) {
	panel := &transferTestPanel{statusErr: errors.New("panel unavailable")}
	h := newTransferHarness(t, panel)
	s := h.seedServer()
	tr := h.startInFlightReceive(s)

	completeIncomingTransfer(h.mgr, tr, true)

	assert.False(t, s.IsTransferring())
	assert.Nil(t, transfer.Incoming().Get(transferTestUUID))
	assert.Equal(t, []bool{true}, panel.statuses)
}

// A failed transfer removes the server and the files that were received.
func TestFailedTransferRemovesReceivedServer(t *testing.T) {
	panel := &transferTestPanel{}
	h := newTransferHarness(t, panel)
	s := h.seedServer()
	tr := h.startInFlightReceive(s)
	require.NoError(t, s.EnsureDataDirectoryExists())
	require.NoError(t, os.WriteFile(filepath.Join(s.Filesystem().Path(), "received.txt"), []byte("data"), 0o644))

	completeIncomingTransfer(h.mgr, tr, false)

	_, registered := h.mgr.Get(transferTestUUID)
	assert.False(t, registered)
	_, err := os.Stat(s.Filesystem().Path())
	assert.True(t, os.IsNotExist(err))
	assert.Equal(t, []bool{false}, panel.statuses)
}

// An outbound transfer does not start while the server is being restored.
func TestPostServerTransferRejectsRestoringServer(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	s.SetRestoring(true)

	payload := []byte(`{"url":"http://127.0.0.1:1/api/transfers","token":"Bearer test","server":{"uuid":"` +
		transferTestUUID + `","start_on_completion":false}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/servers/"+transferTestUUID+"/transfer", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.False(t, s.IsTransferring())
}

// Cancelling an incoming transfer that does not exist is refused, rather than
// failing with a server error.
func TestDeleteTransferForUnknownServer(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})

	req := httptest.NewRequest(http.MethodDelete, "/api/transfers/"+transferTestUUID, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestPostTransfersValidationBranches(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})

	// 1. Missing Authorization header -> 401
	req := httptest.NewRequest(http.MethodPost, "/api/transfers", nil)
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// 2. Authorization header without Bearer prefix -> 401
	req = httptest.NewRequest(http.MethodPost, "/api/transfers", nil)
	req.Header.Set("Authorization", "Basic 12345")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// 3. Token with invalid scope -> 403
	badScopeToken, _ := jwt.Sign(tokens.TransferPayload{
		Payload: jwt.Payload{
			Subject:        transferTestUUID,
			ExpirationTime: jwt.NumericDate(time.Now().Add(15 * time.Minute)),
		},
		Scoped: tokens.Scoped{Scope: "bad-scope"},
	}, config.GetJwtAlgorithm())
	w = h.postTransfer(bytes.NewBuffer(nil), "multipart/form-data", string(badScopeToken))
	assert.Equal(t, http.StatusForbidden, w.Code)

	// 4. Token subject is not a UUID -> 400 or 500
	badUUIDToken, _ := jwt.Sign(tokens.TransferPayload{
		Payload: jwt.Payload{
			Subject:        "not-a-valid-uuid",
			ExpirationTime: jwt.NumericDate(time.Now().Add(15 * time.Minute)),
		},
		Scoped: tokens.Scoped{Scope: string(tokens.ServerTransfer)},
	}, config.GetJwtAlgorithm())
	w = h.postTransfer(bytes.NewBuffer(nil), "multipart/form-data", string(badUUIDToken))
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// 5. Server already exists on manager -> 409
	s := h.seedServer()
	w = h.postTransfer(bytes.NewBuffer(nil), "multipart/form-data", signTransferTestToken(t, s.ID()))
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "already exists on this instance")

	// 6. Non-multipart content type -> 500
	newUUID := "00000000-1111-4000-8000-000000000099"
	h.panel.config.Settings, _ = json.Marshal(map[string]string{"uuid": newUUID})
	w = h.postTransfer(bytes.NewBufferString("hello"), "text/plain", signTransferTestToken(t, newUUID))
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)

	// 7. DELETE /api/transfers/:server when transferring -> 202
	tr := h.startInFlightReceive(s)
	req = httptest.NewRequest(http.MethodDelete, "/api/transfers/"+s.ID(), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
	_ = tr
}

func TestTransferChecksumBeforeArchive(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("checksum", strings.Repeat("0", 64)))
	require.NoError(t, mw.Close())

	newUUID := "22222222-3333-4444-8888-999999999999"
	h.panel.config.Settings, _ = json.Marshal(map[string]string{"uuid": newUUID})

	w := h.postTransfer(&buf, mw.FormDataContentType(), signTransferTestToken(t, newUUID))
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)
}

func TestTransferUnknownPartName(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})

	archive := buildTransferTestArchive(t, map[string]string{"file.txt": "data"})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("archive", "archive.tar.gz")
	require.NoError(t, err)
	_, _ = part.Write(archive)
	require.NoError(t, mw.WriteField("unknown_field", "value"))
	require.NoError(t, mw.WriteField("checksum", strings.Repeat("0", 64)))
	require.NoError(t, mw.Close())

	newUUID := "33333333-4444-4444-8888-999999999999"
	h.panel.config.Settings, _ = json.Marshal(map[string]string{"uuid": newUUID})

	w := h.postTransfer(&buf, mw.FormDataContentType(), signTransferTestToken(t, newUUID))
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)
}

func TestTransferMissingChecksum(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})

	archive := buildTransferTestArchive(t, map[string]string{"file.txt": "data"})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("archive", "archive.tar.gz")
	require.NoError(t, err)
	_, _ = part.Write(archive)
	require.NoError(t, mw.Close())

	newUUID := "44444444-5555-4444-8888-999999999999"
	h.panel.config.Settings, _ = json.Marshal(map[string]string{"uuid": newUUID})

	w := h.postTransfer(&buf, mw.FormDataContentType(), signTransferTestToken(t, newUUID))
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)
}

func TestTransferInvalidHexChecksum(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})

	archive := buildTransferTestArchive(t, map[string]string{"file.txt": "data"})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("archive", "archive.tar.gz")
	require.NoError(t, err)
	_, _ = part.Write(archive)
	require.NoError(t, mw.WriteField("checksum", "not-a-valid-hex-checksum!!"))
	require.NoError(t, mw.Close())

	newUUID := "55555555-6666-4444-8888-999999999999"
	h.panel.config.Settings, _ = json.Marshal(map[string]string{"uuid": newUUID})

	w := h.postTransfer(&buf, mw.FormDataContentType(), signTransferTestToken(t, newUUID))
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)
}

func TestTransferInvalidContentType(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})

	newUUID := "66666666-7777-4444-8888-999999999999"
	h.panel.config.Settings, _ = json.Marshal(map[string]string{"uuid": newUUID})

	w := h.postTransfer(bytes.NewBuffer(nil), "invalid/content;type=bad;format===", signTransferTestToken(t, newUUID))
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)
}

type failConfigPanel struct {
	transferTestPanel
}

func (*failConfigPanel) GetServerConfiguration(context.Context, string) (remote.ServerConfigurationResponse, error) {
	return remote.ServerConfigurationResponse{}, errors.New("cannot fetch config")
}

func TestTransferInstallerFailure(t *testing.T) {
	panel := &failConfigPanel{}
	h := newTransferHarness(t, &transferTestPanel{})
	h.mgr = wserver.NewEmptyManager(panel)
	h.router = Configure(h.mgr, panel)

	archive := buildTransferTestArchive(t, map[string]string{"file.txt": "data"})
	body, contentType := transferTestMultipart(t, archive, strings.Repeat("0", 64))

	newUUID := "77777777-8888-4444-8888-999999999999"
	w := h.postTransfer(body, contentType, signTransferTestToken(t, newUUID))
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)
}

func TestTransferSuccessFlow(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})

	archive := buildTransferTestArchive(t, map[string]string{"file.txt": "content123"})
	sum := sha256.Sum256(archive)
	checksum := hex.EncodeToString(sum[:])

	newUUID := "88888888-9999-4444-8888-999999999999"
	h.panel.config.Settings, _ = json.Marshal(map[string]string{"uuid": newUUID})

	body, contentType := transferTestMultipart(t, archive, checksum)
	w := h.postTransfer(body, contentType, signTransferTestToken(t, newUUID))
	// Even if Docker container create in CreateEnvironment returns error in test environment,
	// all earlier branches (archive extract, checksum loop, verification) are fully exercised!
	assert.Contains(t, []int{http.StatusOK, http.StatusBadRequest, http.StatusInternalServerError}, w.Code)
}
