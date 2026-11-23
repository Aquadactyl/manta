package transfer

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Aquadactyl/manta/config"
	"github.com/Aquadactyl/manta/internal/progress"
	"github.com/Aquadactyl/manta/server/filesystem"
)

type countedTransferBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *countedTransferBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *countedTransferBody) Close() error { b.closed = true; return nil }

func TestTransferBoundsDestinationAcknowledgement(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	for _, test := range []struct {
		name     string
		length   int64
		size     int
		tooLarge bool
		wantRead int
	}{
		{"declared oversized", maxTransferResponseSize + 1, maxTransferResponseSize + 128, true, 0},
		{"unknown length oversized", -1, maxTransferResponseSize + 128, true, maxTransferResponseSize + 1},
		{"exact limit", maxTransferResponseSize, maxTransferResponseSize, false, maxTransferResponseSize},
		{"normal acknowledgement", -1, 8, false, 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys, err := filesystem.New(t.TempDir(), 0, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, fsys.Close()) })
			tr := newManagerTestTransfer(t, "response-limit")
			tr.archive = &Archive{archive: &filesystem.Archive{Filesystem: fsys, Progress: progress.NewProgress(0)}}
			body := &countedTransferBody{Reader: strings.NewReader(strings.Repeat("x", test.size))}
			previous := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = previous })
			http.DefaultTransport = transferRoundTripper(func(req *http.Request) (*http.Response, error) {
				defer req.Body.Close()
				if _, err := io.Copy(io.Discard, req.Body); err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: http.StatusOK, ContentLength: test.length, Body: body, Header: make(http.Header), Request: req}, nil
			})
			out, err := tr.PushArchiveToTarget("http://destination.invalid/api/transfers", "test-token")
			if test.tooLarge {
				require.ErrorIs(t, err, errTransferResponseTooLarge)
				require.Nil(t, out)
			} else {
				require.NoError(t, err)
				require.Len(t, out, test.size)
			}
			require.Equal(t, test.wantRead, body.read)
			require.True(t, body.closed)
			require.NoError(t, tr.Context().Err(), "only the request context should be released on return")
		})
	}
}
