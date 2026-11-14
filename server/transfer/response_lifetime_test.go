package transfer

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/progress"
	"github.com/pterodactyl/wings/server/filesystem"
)

type transferRoundTripper func(*http.Request) (*http.Response, error)

func (f transferRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type transferResponseBody struct {
	io.Reader
	closed bool
}

func (b *transferResponseBody) Close() error {
	b.closed = true
	return nil
}

func TestArchiveStreamCancellationClosesSourcePipe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src, dst := io.Pipe()
	defer src.Close()
	defer dst.Close()
	finished := make(chan struct{})
	done := watchArchiveStreamCancellation(ctx, src, finished)

	cancel()
	<-done

	_, err := dst.Write([]byte("archive footer"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestTransferClosesResponseOnEveryExit(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	for _, test := range []struct {
		name    string
		status  int
		cancel  bool
		wantErr string
	}{
		{name: "destination rejection", status: 503, wantErr: "unexpected status code"},
		{name: "cancel after response", status: 200, cancel: true, wantErr: "context canceled"},
		{name: "success", status: 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys, err := filesystem.New(t.TempDir(), 0, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, fsys.UnixFS().Close()) })
			tr := newManagerTestTransfer(t, "response-lifetime")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tr.ctx = ctx
			tr.archive = &Archive{archive: &filesystem.Archive{Filesystem: fsys, Progress: progress.NewProgress(0)}}
			body := &transferResponseBody{Reader: strings.NewReader("accepted")}
			previous := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = previous })
			http.DefaultTransport = transferRoundTripper(func(req *http.Request) (*http.Response, error) {
				defer req.Body.Close()
				if test.cancel {
					// A transport must close the request body even when it
					// returns before consuming the upload.
					cancel()
				} else if _, copyErr := io.Copy(io.Discard, req.Body); copyErr != nil {
					return nil, copyErr
				}
				return &http.Response{StatusCode: test.status, Body: body, Header: make(http.Header), Request: req}, nil
			})
			out, err := tr.PushArchiveToTarget("http://destination.invalid/api/transfers", "test-token")
			if test.cancel {
				require.Error(t, err)
			} else if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, "accepted", string(out))
			}
			require.True(t, body.closed, "response body must close on success, rejection, and cancellation")
		})
	}
}
