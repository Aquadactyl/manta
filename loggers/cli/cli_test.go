package cli

import (
	"bytes"
	"errors"
	"testing"

	"github.com/apex/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCliHandler(t *testing.T) {
	var buf bytes.Buffer
	h := New(&buf, false)
	assert.NotNil(t, h)

	// Log with info
	entry := &log.Entry{
		Level:   log.InfoLevel,
		Message: "test message",
		Fields: log.Fields{
			"key": "value",
		},
	}
	require.NoError(t, h.HandleLog(entry))
	assert.Contains(t, buf.String(), "test message")
	assert.Contains(t, buf.String(), "key=value")

	// Log with error field
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
}

