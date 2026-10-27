package parser

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/buger/jsonparser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/ufs"
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

func TestParserUnmarshalAndWildcard(t *testing.T) {
	// 1. Missing fields in ConfigurationFile.UnmarshalJSON
	var cfBad ConfigurationFile
	assert.Error(t, json.Unmarshal([]byte(`{"file":"a.txt"}`), &cfBad))
	assert.Error(t, json.Unmarshal([]byte(`{"parser":"json"}`), &cfBad))
	assert.Error(t, json.Unmarshal([]byte(`{"replace":[]}`), &cfBad))
	assert.Error(t, json.Unmarshal([]byte(`"not-an-object"`), &cfBad))

	// 2. Fallback to "value" in ConfigurationFileReplacement.UnmarshalJSON
	var cfr ConfigurationFileReplacement
	require.NoError(t, json.Unmarshal([]byte(`{"match":"port","value":25565}`), &cfr))
	assert.Equal(t, "port", cfr.Match)
	assert.Equal(t, "25565", cfr.ReplaceWith.String())

	// 3. scalarString data types
	strVal, err := scalarString([]byte(`{"num": 123}`), "num")
	require.NoError(t, err)
	assert.Equal(t, "123", strVal)

	boolVal, err := scalarString([]byte(`{"bool": true}`), "bool")
	require.NoError(t, err)
	assert.Equal(t, "true", boolVal)

	nullVal, err := scalarString([]byte(`{"null": null}`), "null")
	require.NoError(t, err)
	assert.Equal(t, "", nullVal)

	_, err = scalarString([]byte(`{"arr": [1, 2]}`), "arr")
	assert.Error(t, err)

	// 4. Wildcard and array parsing in IterateOverJson
	jsonData := []byte(`{
		"items": [
			{"name": "item1", "val": 10},
			{"name": "item2", "val": 20}
		],
		"indexed": [
			{"inner": "a"},
			{"inner": "b"}
		]
	}`)

	var cfWildcard ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{
		"file": "test.json",
		"parser": "json",
		"replace": [
			{"match": "items.*.val", "replace_with": 99},
			{"match": "indexed[1].inner", "replace_with": "updated_b"}
		]
	}`), &cfWildcard))

	c, err := cfWildcard.IterateOverJson(jsonData)
	require.NoError(t, err)
	assert.NotNil(t, c)
	assert.Contains(t, c.String(), "99")
	assert.Contains(t, c.String(), "updated_b")
}

type mockSizeCheckerFile struct {
	ufs.File
	failCheck bool
}

func (m *mockSizeCheckerFile) CheckSize(size int64) error {
	if m.failCheck {
		return errors.New("disk space exceeded")
	}
	return nil
}

func TestParserMoreCases(t *testing.T) {
	// 1. UnmarshalJSON errors for ConfigurationFile
	var cf ConfigurationFile
	assert.Error(t, json.Unmarshal([]byte(`{}`), &cf))
	assert.Error(t, json.Unmarshal([]byte(`{"file":"a","parser":"json"}`), &cf))
	assert.Error(t, json.Unmarshal([]byte(`{"file":123,"parser":"json","replace":[]}`), &cf))
	assert.Error(t, json.Unmarshal([]byte(`{"file":"a","parser":123,"replace":[]}`), &cf))

	// Invalid replace JSON gracefully sets empty replace slice
	require.NoError(t, json.Unmarshal([]byte(`{"file":"a","parser":"json","replace":"not an array"}`), &cf))
	assert.Empty(t, cf.Replace)

	// 2. ConfigurationFileReplacement UnmarshalJSON types
	var cfr ConfigurationFileReplacement
	require.NoError(t, json.Unmarshal([]byte(`{"match": 25565, "if_value": true, "replace_with": "val"}`), &cfr))
	assert.Equal(t, "25565", cfr.Match)
	assert.Equal(t, "true", cfr.IfValue)

	require.NoError(t, json.Unmarshal([]byte(`{"match": false, "if_value": null, "replace_with": "val"}`), &cfr))
	assert.Equal(t, "false", cfr.Match)
	assert.Equal(t, "", cfr.IfValue)

	assert.Error(t, json.Unmarshal([]byte(`{"match": {"nested":"obj"}, "replace_with": "val"}`), &cfr))

	// 3. Rewrite limits and sizeChecker
	tmp := t.TempDir()
	p := filepath.Join(tmp, "test.txt")
	require.NoError(t, os.WriteFile(p, []byte("hello"), 0644))
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	require.NoError(t, err)
	defer f.Close()

	// Exceed maxConfigFileSize
	tooBig := make([]byte, maxConfigFileSize+1)
	assert.Error(t, rewrite(f, tooBig))

	// Mock sizeChecker failure
	mockFile := &mockSizeCheckerFile{File: f, failCheck: true}
	assert.Error(t, rewrite(mockFile, []byte("data")))

	// 4. XML Parsing with attributes and empty doc
	xmlPath := filepath.Join(tmp, "config.xml")
	require.NoError(t, os.WriteFile(xmlPath, []byte(`<ServerSettings><ServerPort>25565</ServerPort></ServerSettings>`), 0644))

	var cfXml ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{
		"file": "config.xml",
		"parser": "xml",
		"replace": [
			{"match": "ServerSettings.ServerPort", "replace_with": "[attr='true']"},
			{"match": "ServerSettings.NewTag.Inner", "replace_with": "inner_value"}
		]
	}`), &cfXml))

	fx, err := os.OpenFile(xmlPath, os.O_RDWR, 0)
	require.NoError(t, err)
	require.NoError(t, cfXml.Parse(fx))
	_ = fx.Close()

	xmlBytes, err := os.ReadFile(xmlPath)
	require.NoError(t, err)
	assert.Contains(t, string(xmlBytes), `attr="true"`)
	assert.Contains(t, string(xmlBytes), "inner_value")

	// 5. INI Parsing with brackets and missing sections
	iniPath := filepath.Join(tmp, "test.ini")
	require.NoError(t, os.WriteFile(iniPath, []byte("[default]\nport = 25565\n"), 0644))

	var cfIni ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{
		"file": "test.ini",
		"parser": "ini",
		"replace": [
			{"match": "[default].port", "replace_with": "25566"},
			{"match": "newsection.key", "replace_with": "val"}
		]
	}`), &cfIni))

	fi, err := os.OpenFile(iniPath, os.O_RDWR, 0)
	require.NoError(t, err)
	require.NoError(t, cfIni.Parse(fi))
	_ = fi.Close()

	iniBytes, err := os.ReadFile(iniPath)
	require.NoError(t, err)
	assert.Contains(t, string(iniBytes), "25566")
	assert.Contains(t, string(iniBytes), "[newsection]")
}

func TestParserErrorPaths(t *testing.T) {
	tmp := t.TempDir()

	// 1. Invalid JSON
	badJsonPath := filepath.Join(tmp, "bad.json")
	require.NoError(t, os.WriteFile(badJsonPath, []byte(`{"key": `), 0644))
	f, err := os.OpenFile(badJsonPath, os.O_RDWR, 0)
	require.NoError(t, err)
	defer f.Close()

	var cfJson ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{"file":"bad.json","parser":"json","replace":[{"match":"key","replace_with":"v"}]}`), &cfJson))
	assert.Error(t, cfJson.Parse(f))

	// 2. Invalid YAML
	badYamlPath := filepath.Join(tmp, "bad.yaml")
	require.NoError(t, os.WriteFile(badYamlPath, []byte(`[invalid: {yaml`), 0644))
	fYaml, err := os.OpenFile(badYamlPath, os.O_RDWR, 0)
	require.NoError(t, err)
	defer fYaml.Close()

	var cfYaml ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{"file":"bad.yaml","parser":"yaml","replace":[{"match":"key","replace_with":"v"}]}`), &cfYaml))
	assert.Error(t, cfYaml.Parse(fYaml))

	// 3. Invalid XML
	badXmlPath := filepath.Join(tmp, "bad.xml")
	require.NoError(t, os.WriteFile(badXmlPath, []byte(`<Server><Unclosed`), 0644))
	fXml, err := os.OpenFile(badXmlPath, os.O_RDWR, 0)
	require.NoError(t, err)
	defer fXml.Close()

	var cfXml ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{"file":"bad.xml","parser":"xml","replace":[{"match":"Server.Port","replace_with":"8080"}]}`), &cfXml))
	assert.Error(t, cfXml.Parse(fXml))

	// 4. Invalid INI
	badIniPath := filepath.Join(tmp, "bad.ini")
	require.NoError(t, os.WriteFile(badIniPath, []byte(`[section`), 0644))
	fIni, err := os.OpenFile(badIniPath, os.O_RDWR, 0)
	require.NoError(t, err)
	defer fIni.Close()

	var cfIni ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{"file":"bad.ini","parser":"ini","replace":[{"match":"section.key","replace_with":"val"}]}`), &cfIni))
	assert.Error(t, cfIni.Parse(fIni))

	// 5. Rewrite failure in file parser (opened read-only)
	txtPath := filepath.Join(tmp, "ro.txt")
	require.NoError(t, os.WriteFile(txtPath, []byte("match_me\n"), 0644))
	fTxt, err := os.Open(txtPath)
	require.NoError(t, err)
	defer fTxt.Close()

	var cfTxt ConfigurationFile
	require.NoError(t, json.Unmarshal([]byte(`{"file":"ro.txt","parser":"file","replace":[{"match":"match_me","replace_with":"val"}]}`), &cfTxt))
	assert.Error(t, cfTxt.Parse(fTxt))

	// 6. getKeyValue with boolean
	var cfrBool ConfigurationFileReplacement
	require.NoError(t, json.Unmarshal([]byte(`{"match":"test","replace_with":true}`), &cfrBool))
	assert.Equal(t, true, cfrBool.getKeyValue("true"))
	assert.Equal(t, false, cfrBool.getKeyValue("false"))
}


