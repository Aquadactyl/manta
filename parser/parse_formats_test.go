package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/buger/jsonparser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

func TestReplaceValueTypes(t *testing.T) {
	rvString := ReplaceValue{value: []byte(`hello\u0021`), valueType: jsonparser.String}
	assert.Equal(t, "hello!", rvString.String())
	assert.Equal(t, []byte("hello!"), rvString.Bytes())
	assert.Equal(t, jsonparser.String, rvString.Type())
	assert.NotEmpty(t, rvString.Value())

	rvNull := ReplaceValue{value: []byte("null"), valueType: jsonparser.Null}
	assert.Equal(t, "<nil>", rvNull.String())
	assert.Equal(t, []byte("<nil>"), rvNull.Bytes())

	rvBool := ReplaceValue{value: []byte("true"), valueType: jsonparser.Boolean}
	assert.Equal(t, "true", rvBool.String())
	assert.Equal(t, []byte("true"), rvBool.Bytes())

	rvNum := ReplaceValue{value: []byte("42"), valueType: jsonparser.Number}
	assert.Equal(t, "42", rvNum.String())
	assert.Equal(t, []byte("42"), rvNum.Bytes())

	rvUnknown := ReplaceValue{value: []byte("?"), valueType: jsonparser.Unknown}
	assert.Equal(t, "<invalid>", rvUnknown.String())
	assert.Equal(t, []byte("<invalid>"), rvUnknown.Bytes())

	var cp ConfigurationParser = "properties"
	assert.Equal(t, "properties", cp.String())
}

func TestParseFormats(t *testing.T) {
	// Initialize minimal config with authentication token for JWT
	config.Set(&config.Configuration{
		AuthenticationToken: "test-auth-token-12345678901234567890",
	})

	tmp := t.TempDir()

	// 1. JSON Parsing
	jsonPath := filepath.Join(tmp, "server.json")
	require.NoError(t, os.WriteFile(jsonPath, []byte(`{"server":{"port":25565,"online":true}}`), 0644))

	var cfJson ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{
		"file": "server.json",
		"parser": "json",
		"replace": [
			{"match": "server.port", "replace_with": 25566},
			{"match": "server.name", "replace_with": "TestServer"}
		]
	}`), &cfJson))

	fJson, err := os.OpenFile(jsonPath, os.O_RDWR, 0)
	require.NoError(t, err)
	require.NoError(t, cfJson.Parse(fJson))
	fJson.Close()

	updatedJson, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	assert.Contains(t, string(updatedJson), "25566")
	assert.Contains(t, string(updatedJson), "TestServer")

	// 2. YAML Parsing
	yamlPath := filepath.Join(tmp, "server.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("server:\n  port: 25565\n"), 0644))

	var cfYaml ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{
		"file": "server.yaml",
		"parser": "yaml",
		"replace": [
			{"match": "server.port", "replace_with": 25567}
		]
	}`), &cfYaml))

	fYaml, err := os.OpenFile(yamlPath, os.O_RDWR, 0)
	require.NoError(t, err)
	require.NoError(t, cfYaml.Parse(fYaml))
	fYaml.Close()

	updatedYaml, err := os.ReadFile(yamlPath)
	require.NoError(t, err)
	assert.Contains(t, string(updatedYaml), "25567")

	// 3. INI Parsing
	iniPath := filepath.Join(tmp, "server.ini")
	require.NoError(t, os.WriteFile(iniPath, []byte("[server]\nport = 25565\n"), 0644))

	var cfIni ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{
		"file": "server.ini",
		"parser": "ini",
		"replace": [
			{"match": "server.port", "replace_with": 25568},
			{"match": "network.bind", "replace_with": "0.0.0.0"}
		]
	}`), &cfIni))

	fIni, err := os.OpenFile(iniPath, os.O_RDWR, 0)
	require.NoError(t, err)
	require.NoError(t, cfIni.Parse(fIni))
	fIni.Close()

	updatedIni, err := os.ReadFile(iniPath)
	require.NoError(t, err)
	assert.Contains(t, string(updatedIni), "25568")
	assert.Contains(t, string(updatedIni), "0.0.0.0")

	// 4. Properties Parsing
	propPath := filepath.Join(tmp, "server.properties")
	require.NoError(t, os.WriteFile(propPath, []byte("# Minecraft server properties\nserver-port=25565\nmotd=Hello\n"), 0644))

	var cfProp ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{
		"file": "server.properties",
		"parser": "properties",
		"replace": [
			{"match": "server-port", "replace_with": 25569},
			{"match": "motd", "if_value": "Hello", "replace_with": "World"},
			{"match": "unused", "if_value": "NonExistent", "replace_with": "Skip"}
		]
	}`), &cfProp))

	fProp, err := os.OpenFile(propPath, os.O_RDWR, 0)
	require.NoError(t, err)
	require.NoError(t, cfProp.Parse(fProp))
	fProp.Close()

	updatedProp, err := os.ReadFile(propPath)
	require.NoError(t, err)
	assert.Contains(t, string(updatedProp), "server-port=25569")
	assert.Contains(t, string(updatedProp), "motd=World")

	// 5. Plain File (Text) find & replace
	txtPath := filepath.Join(tmp, "server.txt")
	require.NoError(t, os.WriteFile(txtPath, []byte("port=25565\nkeep=1\n"), 0644))

	var cfTxt ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{
		"file": "server.txt",
		"parser": "file",
		"replace": [
			{"match": "port=", "replace_with": "port=25570"}
		]
	}`), &cfTxt))

	fTxt, err := os.OpenFile(txtPath, os.O_RDWR, 0)
	require.NoError(t, err)
	require.NoError(t, cfTxt.Parse(fTxt))
	fTxt.Close()

	updatedTxt, err := os.ReadFile(txtPath)
	require.NoError(t, err)
	assert.Contains(t, string(updatedTxt), "port=25570")
	assert.Contains(t, string(updatedTxt), "keep=1")

	// 6. XML Parsing via Parse()
	xmlPath := filepath.Join(tmp, "server.xml")
	require.NoError(t, os.WriteFile(xmlPath, []byte("<server><port>25565</port></server>"), 0644))

	var cfXml ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{
		"file": "server.xml",
		"parser": "xml",
		"replace": [
			{"match": "server.port", "replace_with": 25571}
		]
	}`), &cfXml))

	fXml, err := os.OpenFile(xmlPath, os.O_RDWR, 0)
	require.NoError(t, err)
	require.NoError(t, cfXml.Parse(fXml))
	fXml.Close()
}

func TestParseFileTooLarge(t *testing.T) {
	tmp := t.TempDir()
	largePath := filepath.Join(tmp, "large.txt")
	f, err := os.Create(largePath)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(maxConfigFileSize+100))
	f.Close()

	f, err = os.OpenFile(largePath, os.O_RDWR, 0)
	require.NoError(t, err)
	defer f.Close()

	var cf ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{"file":"large.txt","parser":"file","replace":[]}`), &cf))
	err = cf.Parse(f)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds limit")
}
