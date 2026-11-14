package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/environment/docker"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/filesystem"
)

// setTestConfig stores a configuration for tests that use the global one, so
// they do not depend on another test having set it first.
func setTestConfig(t *testing.T) {
	t.Helper()
	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuthenticationToken = "test-token"
	config.Set(cfg)
}

func serverConfigurationResponse(t *testing.T, uuid string) remote.ServerConfigurationResponse {
	t.Helper()
	settings, err := json.Marshal(map[string]string{"uuid": uuid})
	if err != nil {
		t.Fatal(err)
	}
	return remote.ServerConfigurationResponse{
		Settings:             settings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	}
}

func TestInitServerRejectsInvalidUuid(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.NewAtPath(filepath.Join(root, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuthenticationToken = "test-token"
	cfg.System.Data = filepath.Join(root, "volumes")
	config.Set(cfg)

	m := NewEmptyManager(nil)
	for _, uuid := range []string{"", "..", "../other", "/srv", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/../../other-nested"} {
		t.Run(uuid, func(t *testing.T) {
			if s, err := m.InitServer(uuid, serverConfigurationResponse(t, uuid)); err == nil {
				t.Fatalf("expected an error for uuid %q, server root is %s", uuid, s.Filesystem().Path())
			}
		})
	}
	for _, p := range []string{"other", "other-nested"} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Errorf("expected %s to not be created outside of the data directory", p)
		}
	}
}

func TestInitServerRejectsConfigurationForDifferentServer(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.NewAtPath(filepath.Join(root, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuthenticationToken = "test-token"
	cfg.System.Data = filepath.Join(root, "volumes")
	config.Set(cfg)

	m := NewEmptyManager(nil)
	requested, returned := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if _, err := m.InitServer(requested, serverConfigurationResponse(t, returned)); err == nil {
		t.Fatal("expected an error when the Panel returns the configuration for a different server")
	}
	if _, err := os.Stat(filepath.Join(root, "volumes", returned)); !os.IsNotExist(err) {
		t.Fatal("expected no data directory to be created for the other server")
	}
}

func TestInitServerReturnsConstructionErrors(t *testing.T) {
	setTestConfig(t)
	root := t.TempDir()
	config.Get().System.RootDirectory = root
	config.Get().System.Data = filepath.Join(root, "volumes")
	oldFilesystem := newServerFilesystem
	newServerFilesystem = func(_ string, size int64, denylist []string) (*filesystem.Filesystem, error) {
		return oldFilesystem(filepath.Join(t.TempDir(), "server"), size, denylist)
	}
	t.Cleanup(func() { newServerFilesystem = oldFilesystem })
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

	t.Run("server defaults", func(t *testing.T) {
		old := setServerDefaults
		want := errors.New("defaults failed")
		setServerDefaults = func(any) error { return want }
		t.Cleanup(func() { setServerDefaults = old })
		if _, err := NewEmptyManager(nil).InitServer(id, serverConfigurationResponse(t, id)); !errors.Is(err, want) {
			t.Fatalf("expected defaults error, got %v", err)
		}
	})

	t.Run("Docker environment", func(t *testing.T) {
		old := newServerEnvironment
		want := errors.New("Docker construction failed")
		newServerEnvironment = func(string, *docker.Metadata, *environment.Configuration) (*docker.Environment, error) {
			return nil, want
		}
		t.Cleanup(func() { newServerEnvironment = old })
		if _, err := NewEmptyManager(nil).InitServer(id, serverConfigurationResponse(t, id)); !errors.Is(err, want) {
			t.Fatalf("expected Docker construction error, got %v", err)
		}
	})
}

func TestManagerAddIfMissing(t *testing.T) {
	setTestConfig(t)
	m := NewEmptyManager(nil)
	newServer := func() *Server {
		s, err := New(nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SyncWithConfiguration(serverConfigurationResponse(t, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")); err != nil {
			t.Fatal(err)
		}
		return s
	}

	if !m.AddIfMissing(newServer()) {
		t.Fatal("expected server to be added")
	}
	if m.AddIfMissing(newServer()) {
		t.Fatal("expected a second server with the same uuid to not be added")
	}
	if m.Len() != 1 {
		t.Fatalf("expected one server, got %d", m.Len())
	}
}

func TestSyncWithConfigurationRejectsChangedUuid(t *testing.T) {
	setTestConfig(t)
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncWithConfiguration(serverConfigurationResponse(t, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncWithConfiguration(serverConfigurationResponse(t, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")); err == nil {
		t.Fatal("expected an error when the Panel returns a different uuid for a server")
	}
	if s.ID() != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("expected server uuid to be unchanged, got %q", s.ID())
	}
}
