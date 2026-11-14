package sftp

import (
	"errors"
	"testing"
)

func TestSFTPServerHandleErrorLoggingPaths(t *testing.T) {
	logInboundHandleError(nil, "127.0.0.1")
	logInboundHandleError(errors.New("connection handling failed"), "127.0.0.1")
	logSessionHandleError(nil, "user")
	logSessionHandleError(errors.New("session handling failed"), "user")
}
