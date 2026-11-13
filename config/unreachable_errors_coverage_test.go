package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
)

func TestConfigSetupErrorReturns(t *testing.T) {
	oldDefaults, oldMarshal, oldParse := setConfigurationDefaults, marshalConfiguration, parseLogrotateTemplate
	oldLogrotateDirectory := logrotateDirectory
	t.Cleanup(func() {
		setConfigurationDefaults = oldDefaults
		marshalConfiguration = oldMarshal
		parseLogrotateTemplate = oldParse
		logrotateDirectory = oldLogrotateDirectory
	})

	t.Run("NewAtPath and Load return defaults errors", func(t *testing.T) {
		want := errors.New("defaults unavailable")
		setConfigurationDefaults = func(any) error { return want }
		_, err := NewAtPath("config.yml")
		require.ErrorIs(t, err, want)

		path := filepath.Join(t.TempDir(), "config.yml")
		require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
		_, err = Load(path)
		require.ErrorIs(t, err, want)
	})

	t.Run("WriteToDisk returns marshal errors", func(t *testing.T) {
		want := errors.New("marshal failed")
		marshalConfiguration = func(any) ([]byte, error) { return nil, want }
		cfg := &Configuration{path: filepath.Join(t.TempDir(), "config.yml")}
		require.ErrorIs(t, WriteToDisk(cfg), want)
	})

	t.Run("logrotate returns template parse errors", func(t *testing.T) {
		want := errors.New("template parse failed")
		parseLogrotateTemplate = func(string, string) (*template.Template, error) { return nil, want }
		logrotateDirectory = t.TempDir()
		cfg := &Configuration{}
		cfg.System.EnableLogRotate = true
		Set(cfg)
		require.ErrorIs(t, EnableLogRotation(), want)
	})
}
