package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileContentsStreamingBoundariesAndConcurrentReaders(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	require.NoError(t, os.MkdirAll(s.Filesystem().Path(), 0o755))
	for i, size := range []int{0, 17, (64 << 10) - 1, 64 << 10, (64 << 10) + 1, (1 << 20) + 13} {
		name := fmt.Sprintf("payload-%d.bin", i)
		payload := bytes.Repeat([]byte{byte(i + 1)}, size)
		require.NoError(t, os.WriteFile(filepath.Join(s.Filesystem().Path(), name), payload, 0o600))
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			for range 5 {
				req := httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=/"+name+"&download=1", nil)
				req.Header.Set("Authorization", "Bearer test-token")
				res := httptest.NewRecorder()
				h.router.ServeHTTP(res, req)
				require.Equal(t, http.StatusOK, res.Code)
				require.True(t, bytes.Equal(payload, res.Body.Bytes()), "streamed content changed")
				require.Equal(t, strconv.Itoa(size), res.Header().Get("Content-Length"))
				require.Equal(t, "application/octet-stream", res.Header().Get("Content-Type"))
				require.Equal(t, "attachment; filename="+strconv.Quote(name), res.Header().Get("Content-Disposition"))
				require.NotEmpty(t, res.Header().Get("X-Mime-Type"))
			}
		})
	}
}

type growingFileResponse struct {
	*httptest.ResponseRecorder
	grow func()
}

func (w *growingFileResponse) Write(data []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(data)
	if w.grow != nil {
		grow := w.grow
		w.grow = nil
		grow()
	}
	return n, err
}

func TestFileContentsStopsAtOriginalLengthWhenFileGrows(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	require.NoError(t, os.MkdirAll(s.Filesystem().Path(), 0o755))
	path := filepath.Join(s.Filesystem().Path(), "growing.bin")
	before := bytes.Repeat([]byte("old-content"), 17000)
	require.NoError(t, os.WriteFile(path, before, 0o600))
	res := &growingFileResponse{ResponseRecorder: httptest.NewRecorder(), grow: func() {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		require.NoError(t, err)
		_, err = f.Write(bytes.Repeat([]byte("new-content"), 17000))
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/contents?file=/growing.bin", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	h.router.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code)
	require.Equal(t, before, res.Body.Bytes())
	require.Equal(t, strconv.Itoa(len(before)), res.Header().Get("Content-Length"))
}

func TestDirectoryAPIWireFormat(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	root := s.Filesystem().Path()
	require.NoError(t, os.MkdirAll(root, 0o755))
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/list-directory?directory=/", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		res := httptest.NewRecorder()
		h.router.ServeHTTP(res, req)
		require.Equal(t, http.StatusOK, res.Code)
		return res
	}
	require.Equal(t, "[]", request().Body.String())
	name := "<>&\"\n\u96ea.txt"
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...)
	require.NoError(t, os.WriteFile(filepath.Join(root, name), png, 0o640))
	require.NoError(t, os.Mkdir(filepath.Join(root, "z-folder"), 0o750))
	res := request()
	require.Contains(t, res.Body.String(), `\u003c\u003e\u0026`)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &rows))
	require.Len(t, rows, 2)
	require.Equal(t, "z-folder", rows[0]["name"])
	require.Equal(t, true, rows[0]["directory"])
	require.Equal(t, "inode/directory", rows[0]["mime"])
	file := rows[1]
	require.Len(t, file, 10)
	require.Equal(t, name, file["name"])
	require.Equal(t, "image/png", file["mime"])
	require.Equal(t, "-rw-r-----", file["mode"])
	require.Equal(t, "640", file["mode_bits"])
	require.Equal(t, float64(len(png)), file["size"])
	require.Equal(t, false, file["directory"])
	require.Equal(t, true, file["file"])
	require.Equal(t, false, file["symlink"])
	require.NotEmpty(t, file["created"])
	require.NotEmpty(t, file["modified"])
}

func TestServerFileSearchIsRecursiveScopedAndCapped(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	root := s.Filesystem().Path()
	scope := filepath.Join(root, "scope", "nested")
	require.NoError(t, os.MkdirAll(scope, 0o755))
	for i := range 101 {
		name := fmt.Sprintf("Needle-%03d.txt", i)
		require.NoError(t, os.WriteFile(filepath.Join(scope, name), nil, 0o600))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "outside"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "outside", "needle.txt"), nil, 0o600))

	req := httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/files/search?directory=%2Fscope&query=NEEDLE", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	res := httptest.NewRecorder()
	h.router.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code)

	var paths []string
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &paths))
	require.Len(t, paths, fileSearchLimit)
	for _, path := range paths {
		require.True(t, strings.HasPrefix(path, "scope/nested/"), "unexpected result path %q", path)
	}
}
