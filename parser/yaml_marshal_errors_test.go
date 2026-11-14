package parser

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseYamlFileMarshalErrors(t *testing.T) {
	oldJSON, oldYAML := marshalYAMLIntermediateJSON, marshalYAMLResult
	t.Cleanup(func() {
		marshalYAMLIntermediateJSON, marshalYAMLResult = oldJSON, oldYAML
	})

	openYAML := func(t *testing.T) *os.File {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte("key: value\n"), 0o600))
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		require.NoError(t, err)
		t.Cleanup(func() { _ = file.Close() })
		return file
	}

	t.Run("intermediate JSON marshal error", func(t *testing.T) {
		want := errors.New("JSON marshal failed")
		marshalYAMLIntermediateJSON = func(any) ([]byte, error) { return nil, want }
		require.ErrorIs(t, (&ConfigurationFile{}).parseYamlFile(openYAML(t)), want)
	})

	t.Run("final YAML marshal error", func(t *testing.T) {
		marshalYAMLIntermediateJSON = oldJSON
		want := errors.New("YAML marshal failed")
		marshalYAMLResult = func(any) ([]byte, error) { return nil, want }
		require.ErrorIs(t, (&ConfigurationFile{}).parseYamlFile(openYAML(t)), want)
	})
}
