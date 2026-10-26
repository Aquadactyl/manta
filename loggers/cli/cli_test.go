package cli

import (
	"bytes"
	"os"
	"sync"
	"testing"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCliHandler(t *testing.T) {
	var buf bytes.Buffer
	h := New(&buf, false)
	assert.NotNil(t, h)

	// New with os.File
	tmpFile, err := os.CreateTemp(t.TempDir(), "log")
	require.NoError(t, err)
	defer tmpFile.Close()
	hFile := New(tmpFile, true)
	assert.NotNil(t, hFile)

	// Log with info and source field
	entry := &log.Entry{
		Level:   log.InfoLevel,
		Message: "test message",
		Fields: log.Fields{
			"key":    "value",
			"source": "skip-this",
		},
	}
	require.NoError(t, h.HandleLog(entry))
	assert.Contains(t, buf.String(), "test message")
	assert.Contains(t, buf.String(), "key=value")
	assert.NotContains(t, buf.String(), "source=")

	// Log with error field without runtime.goexit
	buf.Reset()
	errEntry := &log.Entry{
		Level:   log.ErrorLevel,
		Message: "error message",
		Fields: log.Fields{
			"error": errors.New("underlying error"),
		},
	}
	require.NoError(t, h.HandleLog(errEntry))
	assert.Contains(t, buf.String(), "error message")
	assert.Contains(t, buf.String(), "underlying error")

	// Log with error created in a goroutine (contains runtime.goexit)
	var stackErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stackErr = errors.WithStack(errors.New("goroutine stack error"))
	}()
	wg.Wait()

	buf.Reset()
	goexitEntry := &log.Entry{
		Level:   log.ErrorLevel,
		Message: "goroutine error",
		Fields: log.Fields{
			"error": stackErr,
		},
	}
	require.NoError(t, h.HandleLog(goexitEntry))
	assert.Contains(t, buf.String(), "goroutine error")
	assert.Contains(t, buf.String(), "goroutine stack error")
}


