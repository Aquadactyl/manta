package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/ufs"
	"github.com/pterodactyl/wings/parser"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/filesystem"
)

type closeErrorConfigurationFile struct {
	ufs.File
	err error
}

func (f closeErrorConfigurationFile) Close() error {
	_ = f.File.Close()
	return f.err
}

func newConfigParserServer(t *testing.T, diskLimit int64, raw string) (*Server, string) {
	t.Helper()
	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	cfg.System.User.Uid = os.Getuid()
	cfg.System.User.Gid = os.Getgid()
	config.Set(cfg)

	root := filepath.Join(t.TempDir(), "server")
	require.NoError(t, os.Mkdir(root, 0o755))
	fs, err := filesystem.New(root, diskLimit, nil)
	require.NoError(t, err)

	var cf parser.ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(raw), &cf))

	s, err := New(nil)
	require.NoError(t, err)
	s.fs = fs
	s.procConfig = &remote.ProcessConfiguration{ConfigurationFiles: []parser.ConfigurationFile{cf}}
	return s, root
}

// Rewriting a configuration file counts against the server's disk limit, so a
// rewrite that would grow the file past it is refused.
func TestConfigurationFilesRespectDiskLimit(t *testing.T) {
	raw := `{"file":"log.txt","parser":"file","replace":[{"match":"k","replace_with":"` + strings.Repeat("x", 1024) + `"}]}`
	s, root := newConfigParserServer(t, 1<<20, raw)
	original := bytes.Repeat([]byte("k\n"), 32768)
	require.NoError(t, os.WriteFile(filepath.Join(root, "log.txt"), original, 0o644))

	_, err := s.Filesystem().DiskUsage(false)
	require.NoError(t, err)

	s.UpdateConfigurationFiles()

	// The file is left as it was rather than partly rewritten.
	b, err := os.ReadFile(filepath.Join(root, "log.txt"))
	require.NoError(t, err)
	require.Equal(t, original, b, "the configuration file grew past the disk limit")
	require.LessOrEqual(t, s.Filesystem().CachedUsage(), int64(1<<20))
}

// Within the disk limit, configuration files are rewritten and counted.
func TestConfigurationFilesAreUpdated(t *testing.T) {
	raw := `{"file":"server.properties","parser":"file","replace":[{"match":"port=","replace_with":"port=25565"}]}`
	s, root := newConfigParserServer(t, 0, raw)
	require.NoError(t, os.WriteFile(filepath.Join(root, "server.properties"), []byte("motd=hi\nport=1\n"), 0o644))

	s.UpdateConfigurationFiles()

	b, err := os.ReadFile(filepath.Join(root, "server.properties"))
	require.NoError(t, err)
	require.Equal(t, "motd=hi\nport=25565\n", string(b))
}

func TestConfigurationFilesMoreCases(t *testing.T) {
	// 1. Text file that does not exist yet
	rawTxt := `{"file":"does_not_exist.txt","parser":"file","replace":[]}`
	s, root := newConfigParserServer(t, 0, rawTxt)
	s.UpdateConfigurationFiles()
	require.NoFileExists(t, filepath.Join(root, "does_not_exist.txt"))

	// 2. Parse error (e.g. invalid json in file)
	rawJson := `{"file":"broken.json","parser":"json","replace":[{"match":"port","value_data":"1234"}]}`
	var cfJson parser.ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(rawJson), &cfJson))
	s.procConfig = &remote.ProcessConfiguration{ConfigurationFiles: []parser.ConfigurationFile{cfJson}}
	require.NoError(t, os.WriteFile(filepath.Join(root, "broken.json"), []byte(`{invalid-json`), 0644))
	s.UpdateConfigurationFiles()
}

func TestConfigurationFilesReportCloseAndChownErrors(t *testing.T) {
	raw := `{"file":"server.properties","parser":"file","replace":[{"match":"port=","replace_with":"port=25565"}]}`
	s, root := newConfigParserServer(t, 0, raw)
	require.NoError(t, os.WriteFile(filepath.Join(root, "server.properties"), []byte("port=1\n"), 0o644))

	oldTouch, oldChown := configurationFileTouch, configurationFileChown
	t.Cleanup(func() {
		configurationFileTouch, configurationFileChown = oldTouch, oldChown
	})
	closeErr, chownErr := errors.New("close failed"), errors.New("chown failed")
	configurationFileTouch = func(fs *filesystem.Filesystem, name string, flag int) (ufs.File, error) {
		f, err := oldTouch(fs, name, flag)
		if err != nil {
			return nil, err
		}
		return closeErrorConfigurationFile{File: f, err: closeErr}, nil
	}
	configurationFileChown = func(*filesystem.Filesystem, string) error { return chownErr }

	s.UpdateConfigurationFiles()

	b, err := os.ReadFile(filepath.Join(root, "server.properties"))
	require.NoError(t, err)
	require.Equal(t, "port=25565\n", string(b))
}
