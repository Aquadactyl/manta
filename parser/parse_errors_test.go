package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

func TestParseRejectsUnknownTypeOversizedAndUnstatableFiles(t *testing.T) {
	config.Set(&config.Configuration{})
	path := filepath.Join(t.TempDir(), "input")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))

	t.Run("unknown parser", func(t *testing.T) {
		f, err := os.Open(path)
		require.NoError(t, err)
		defer f.Close()
		err = (&ConfigurationFile{Parser: "unknown"}).Parse(f)
		require.ErrorContains(t, err, `unknown parser type "unknown"`)
	})

	t.Run("oversized file", func(t *testing.T) {
		large := filepath.Join(t.TempDir(), "large")
		f, err := os.Create(large)
		require.NoError(t, err)
		require.NoError(t, f.Truncate(maxConfigFileSize+1))
		require.ErrorContains(t, (&ConfigurationFile{Parser: Json}).Parse(f), "exceeds limit")
		require.NoError(t, f.Close())
	})

	t.Run("stat failure", func(t *testing.T) {
		f, err := os.Open(path)
		require.NoError(t, err)
		require.NoError(t, f.Close())
		require.Error(t, (&ConfigurationFile{Parser: Json}).Parse(f))
	})
}

func TestParseRejectsMalformedInputForEachParser(t *testing.T) {
	config.Set(&config.Configuration{})
	cases := []struct {
		parser   ConfigurationParser
		contents string
	}{
		{parser: Xml, contents: "<root>"},
		{parser: Ini, contents: "[section\nkey=value"},
		{parser: Json, contents: "{"},
		{parser: Yaml, contents: "key: [unterminated"},
		{parser: Properties, contents: "\\u12"},
	}
	for _, tc := range cases {
		t.Run(tc.parser.String(), func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "input")
			require.NoError(t, os.WriteFile(p, []byte(tc.contents), 0o600))
			f, err := os.OpenFile(p, os.O_RDWR, 0)
			require.NoError(t, err)
			defer f.Close()
			require.Error(t, (&ConfigurationFile{Parser: tc.parser}).Parse(f))
		})
	}
}
