package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/filesystem"
)

const installTestUUID = "5c7a1b9e-2f40-4d8a-9b3e-7a1c2d3e4f50"

// installerCreateRequest is the body of a container create call. The Config
// fields are sent at the top level and the HostConfig under its own key.
type installerCreateRequest struct {
	container.Config
	HostConfig *container.HostConfig `json:"HostConfig"`
}

// fakeInstallerDocker answers the Docker API calls made by the installer code
// path. It records the create body, fails the start call so Execute returns
// without a real container, and serves a fixed log body.
type fakeInstallerDocker struct {
	mu          sync.Mutex
	create      *installerCreateRequest
	logs        string
	startErr    bool
	failPull    bool
	failList    bool
	createErr   bool
	waitErr     bool
	logsErr     bool
	logsCopyErr bool
	deleteErr   bool
	hugeScan    bool
}

func (f *fakeInstallerDocker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/images/create"):
		if f.hugeScan {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("a", 70000)))
			return
		}
		if f.failPull {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"pull failed"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"Downloading"}` + "\n" + `{"status":"Extracting"}` + "\n" + `{"status":"Pull complete"}` + "\n"))
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/json"):
		if f.failList {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"list failed"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"RepoTags":["ghcr.io/pterodactyl/installers:debian"]}]`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create"):
		if f.createErr {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"create failed"}`))
			return
		}
		var req installerCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.create = &req
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"installer-test-id","Warnings":[]}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/start"):
		if f.startErr {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"stopped by test after container create"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/wait"):
		if f.waitErr {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"wait failed"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"StatusCode":0}`))
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/logs"):
		if f.logsErr {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"logs failed"}`))
			return
		}
		if f.logsCopyErr {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("short"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, f.logs)
	case r.Method == http.MethodDelete:
		if f.deleteErr {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"delete failed"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// newInstallerTestProcess builds an installation process whose Docker client
// talks to a fake API server, with a data directory, temp directory and log
// directory that are all private to the test.
func newInstallerTestProcess(t *testing.T, fake *fakeInstallerDocker) *InstallationProcess {
	t.Helper()

	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	c, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(strings.Replace(srv.URL, "http://", "tcp://", 1)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	cfg.System.TmpDirectory = t.TempDir()
	cfg.System.LogDirectory = t.TempDir()
	config.Set(cfg)

	s, err := New(nil)
	require.NoError(t, err)
	s.cfg.Uuid = installTestUUID
	s.fs, err = filesystem.New(filepath.Join(t.TempDir(), installTestUUID), 0, nil)
	require.NoError(t, err)

	return &InstallationProcess{
		Server: s,
		Script: &remote.InstallationScript{
			ContainerImage: "ghcr.io/pterodactyl/installers:debian",
			Entrypoint:     "bash",
			Script:         "echo installing",
		},
		client: c,
	}
}

// captureInstallerCreate runs the installer until its container create call
// and returns the HostConfig and Config that were sent to Docker.
func captureInstallerCreate(t *testing.T) (*container.HostConfig, *container.Config) {
	t.Helper()

	fake := &fakeInstallerDocker{startErr: true}
	p := newInstallerTestProcess(t, fake)

	_, err := p.Execute()
	require.Error(t, err, "the fake daemon fails the start call on purpose")

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.NotNil(t, fake.create, "installer never sent a container create request")
	require.NotNil(t, fake.create.HostConfig)
	return fake.create.HostConfig, &fake.create.Config
}

// The install container runs without the privileges installation scripts do
// not need.
func TestInstallContainerIsRestricted(t *testing.T) {
	hc, _ := captureInstallerCreate(t)

	// The Docker client sends capabilities by their full names.
	for _, capability := range []string{"CAP_NET_RAW", "CAP_MKNOD", "CAP_SETPCAP", "CAP_SYS_CHROOT"} {
		assert.Contains(t, hc.CapDrop, capability)
	}
	assert.Contains(t, hc.SecurityOpt, "no-new-privileges")

	require.NotNil(t, hc.Resources.PidsLimit)
	assert.GreaterOrEqual(t, *hc.Resources.PidsLimit, installerPidLimit)

	for _, m := range hc.Mounts {
		if m.Target == "/mnt/install" {
			assert.True(t, m.ReadOnly, "the installation script should be mounted read-only")
		}
	}
}

// The install log records each environment variable exactly as it was passed to
// the container.
func TestInstallLogKeepsEnvironmentValuesVerbatim(t *testing.T) {
	fake := &fakeInstallerDocker{logs: "installer output\n"}
	p := newInstallerTestProcess(t, fake)
	p.Server.cfg.EnvVars = environment.Variables{"DB_PASSWORD": "p&ss'word<1>"}

	require.NoError(t, os.MkdirAll(filepath.Join(config.Get().System.LogDirectory, "install"), 0o700))
	require.NoError(t, p.AfterExecute("installer-test-id"))

	body, err := os.ReadFile(p.GetLogPath())
	require.NoError(t, err)
	assert.Contains(t, string(body), "DB_PASSWORD=p&ss'word<1>")
}

func TestInstallationProcessFullRun(t *testing.T) {
	fake := &fakeInstallerDocker{logs: "installed successfully\n"}
	p := newInstallerTestProcess(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(config.Get().System.LogDirectory, "install"), 0o700))

	err := p.Run()
	require.NoError(t, err)

	body, err := os.ReadFile(p.GetLogPath())
	require.NoError(t, err)
	assert.Contains(t, string(body), "installed successfully")
}

func TestInstallationProcessAlreadyInstalling(t *testing.T) {
	fake := &fakeInstallerDocker{}
	p := newInstallerTestProcess(t, fake)
	p.Server.installing.Store(true)
	err := p.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot obtain installation lock")
}

func TestInstallationProcessWithRegistryAuth(t *testing.T) {
	fake := &fakeInstallerDocker{logs: "ok\n"}
	p := newInstallerTestProcess(t, fake)

	cfg := config.Get()
	if cfg.Docker.Registries == nil {
		cfg.Docker.Registries = make(map[string]config.RegistryConfiguration)
	}
	cfg.Docker.Registries["ghcr.io"] = config.RegistryConfiguration{
		Username: "testuser",
		Password: "testpassword",
	}
	config.Set(cfg)

	require.NoError(t, os.MkdirAll(filepath.Join(config.Get().System.LogDirectory, "install"), 0o700))
	require.NoError(t, p.pullInstallationImage())
}

func TestInstallationProcessPullFallback(t *testing.T) {
	fake := &fakeInstallerDocker{failPull: true}
	p := newInstallerTestProcess(t, fake)
	require.NoError(t, p.pullInstallationImage())

	// ImageList fails
	fake.failList = true
	assert.Error(t, p.pullInstallationImage())
}

func TestInstallationProcessRemoveContainer(t *testing.T) {
	fake := &fakeInstallerDocker{}
	p := newInstallerTestProcess(t, fake)
	assert.NoError(t, p.RemoveContainer())

	fake.deleteErr = true
	assert.Error(t, p.RemoveContainer())
}

func TestInstallationProcessWriteScriptErrors(t *testing.T) {
	fake := &fakeInstallerDocker{}
	p := newInstallerTestProcess(t, fake)

	// OpenFile fails if install.sh is a directory
	require.NoError(t, os.MkdirAll(filepath.Join(p.tempDir(), "install.sh"), 0755))
	assert.Error(t, p.writeScriptToDisk())
	_ = os.RemoveAll(p.tempDir())

	// MkdirAll fails if TmpDirectory is a regular file
	oldTmp := config.Get().System.TmpDirectory
	tmpFile := filepath.Join(t.TempDir(), "tmp_file")
	require.NoError(t, os.WriteFile(tmpFile, []byte("data"), 0644))
	cfg := config.Get()
	cfg.System.TmpDirectory = tmpFile
	config.Set(cfg)
	assert.Error(t, p.writeScriptToDisk())
	cfg.System.TmpDirectory = oldTmp
	config.Set(cfg)
}

func TestInstallationProcessBeforeExecuteErrors(t *testing.T) {
	fake := &fakeInstallerDocker{}
	p := newInstallerTestProcess(t, fake)

	// writeScriptToDisk error
	require.NoError(t, os.MkdirAll(filepath.Join(p.tempDir(), "install.sh"), 0755))
	assert.Error(t, p.BeforeExecute())
	_ = os.RemoveAll(p.tempDir())

	// pullInstallationImage error (fails when images/create fails and images/json has no matching tag)
	fake.failPull = true
	p.Script.ContainerImage = "nonexistent.registry.local/image:tag"
	assert.Error(t, p.BeforeExecute())
	p.Script.ContainerImage = "ghcr.io/pterodactyl/installers:debian"
	fake.failPull = false

	// RemoveContainer error
	fake.deleteErr = true
	assert.Error(t, p.BeforeExecute())
}

func TestInstallationProcessAfterExecuteErrors(t *testing.T) {
	fake := &fakeInstallerDocker{logsErr: true}
	p := newInstallerTestProcess(t, fake)
	assert.Error(t, p.AfterExecute("container-123"))

	// RemoveContainer error during defer
	fake.logsErr = false
	fake.deleteErr = true
	require.NoError(t, os.MkdirAll(filepath.Join(config.Get().System.LogDirectory, "install"), 0o700))
	assert.NoError(t, p.AfterExecute("container-123"))
}

func TestInstallationProcessExecuteErrors(t *testing.T) {
	fake := &fakeInstallerDocker{createErr: true}
	p := newInstallerTestProcess(t, fake)
	_, err := p.Execute()
	assert.Error(t, err)

	fake.createErr = false
	fake.waitErr = true
	_, err = p.Execute()
	assert.Error(t, err)

	// EnsureDataDirectoryExists error in Execute
	fake.waitErr = false
	testDir := t.TempDir()
	mkdirFailDir := filepath.Join(testDir, "mkdir_fail_parent", "child")
	mkdirFailFs, err := filesystem.New(mkdirFailDir, 0, nil)
	require.NoError(t, err)
	_ = os.RemoveAll(filepath.Join(testDir, "mkdir_fail_parent"))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "mkdir_fail_parent"), []byte("not a dir"), 0644))
	p.Server.fs = mkdirFailFs
	_, err = p.Execute()
	assert.Error(t, err)

	// defer removeAll tempDir failure in Execute
	p2 := newInstallerTestProcess(t, fake)
	require.NoError(t, os.MkdirAll(p2.tempDir(), 0755))
	immTempFile := filepath.Join(p2.tempDir(), "imm.txt")
	require.NoError(t, os.WriteFile(immTempFile, []byte("x"), 0644))
	if exec.Command("chattr", "+i", immTempFile).Run() == nil {
		fake.startErr = true
		_, _ = p2.Execute()
		_ = exec.Command("chattr", "-i", immTempFile).Run()
		fake.startErr = false
	}
}

func TestInstallationProcessStreamOutput(t *testing.T) {
	fake := &fakeInstallerDocker{logsErr: true}
	p := newInstallerTestProcess(t, fake)
	assert.Error(t, p.StreamOutput(context.Background(), "test-id"))

	fake.logsErr = false
	fake.logs = "stream line 1\nstream line 2\n"
	assert.NoError(t, p.StreamOutput(context.Background(), "test-id"))

	fake.logsCopyErr = true
	assert.NoError(t, p.StreamOutput(context.Background(), "test-id"))
}

func TestInstallationProcessRunErrors(t *testing.T) {
	fake := &fakeInstallerDocker{deleteErr: true}
	p := newInstallerTestProcess(t, fake)
	// BeforeExecute fails due to RemoveContainer error
	assert.Error(t, p.Run())

	// Execute fails
	fake.deleteErr = false
	fake.createErr = true
	assert.Error(t, p.Run())

	// AfterExecute fails inside Run()
	fake.createErr = false
	fake.logsErr = true
	assert.NoError(t, p.Run())
}

func TestInstallationProcessAfterExecuteOpenLogFail(t *testing.T) {
	fake := &fakeInstallerDocker{logs: "hello"}
	p := newInstallerTestProcess(t, fake)
	// Block GetLogPath() by making install directory a regular file
	oldLogDir := config.Get().System.LogDirectory
	logDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "install"), []byte("not a dir"), 0644))
	cfg := config.Get()
	cfg.System.LogDirectory = logDir
	config.Set(cfg)

	assert.Error(t, p.AfterExecute("container-123"))

	cfg.System.LogDirectory = oldLogDir
	config.Set(cfg)
}

func TestInstallationProcessAfterExecuteCopyFail(t *testing.T) {
	fake := &fakeInstallerDocker{logsCopyErr: true}
	p := newInstallerTestProcess(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(config.Get().System.LogDirectory, "install"), 0o700))
	assert.Error(t, p.AfterExecute("container-123"))
}

func TestInstallationProcessPullScannerError(t *testing.T) {
	fake := &fakeInstallerDocker{hugeScan: true}
	p := newInstallerTestProcess(t, fake)
	assert.Error(t, p.pullInstallationImage())
}
