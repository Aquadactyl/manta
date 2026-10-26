package ufs

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCountedReader(t *testing.T) {
	src := bytes.NewReader([]byte("testing 12345"))
	cr := NewCountedReader(src)

	buf := make([]byte, 7)
	n, err := cr.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 7, n)
	assert.Equal(t, int64(7), cr.BytesRead())
	assert.NoError(t, cr.Error())

	// Read remaining
	buf2 := make([]byte, 100)
	n2, err := cr.Read(buf2)
	assert.Equal(t, 6, n2)
	assert.Equal(t, int64(13), cr.BytesRead())
	assert.NoError(t, cr.Error()) // EOF is returned as nil error by Error()

	// Read after EOF
	n3, err := cr.Read(buf2)
	assert.Equal(t, 0, n3)
	assert.Equal(t, io.EOF, err)
}

func TestCountedWriter(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewUnixFS(dir, false)
	require.NoError(t, err)
	defer fs.Close()

	f, err := fs.Create("cw_test.txt")
	require.NoError(t, err)
	defer f.Close()

	cw := NewCountedWriter(f)

	n, err := cw.Write([]byte("hello "))
	require.NoError(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, int64(6), cw.BytesWritten())
	assert.NoError(t, cw.Error())

	// ReadFrom
	n2, err := cw.ReadFrom(bytes.NewReader([]byte("world!")))
	require.NoError(t, err)
	assert.Equal(t, int64(6), n2)
	assert.Equal(t, int64(12), cw.BytesWritten())
}

func TestQuotaGettersSetters(t *testing.T) {
	q := NewQuota(nil, 500)
	assert.Equal(t, int64(500), q.Limit())
	q.SetLimit(1000)
	assert.Equal(t, int64(1000), q.Limit())

	assert.Equal(t, int64(0), q.Usage())
	q.SetUsage(250)
	assert.Equal(t, int64(250), q.Usage())
}
