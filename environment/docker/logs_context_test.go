package docker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

type logCancellationTransport func(*http.Request) (*http.Response, error)

func (f logCancellationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type logReadNotifier struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (r *logReadNotifier) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.ReadCloser.Read(p)
}

func TestReadlogContextCancelsRequestAndBody(t *testing.T) {
	for _, phase := range []string{"waiting for headers", "reading body"} {
		t.Run(phase, func(t *testing.T) {
			started, stopped := make(chan struct{}), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == "reading body" {
					w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				if phase == "waiting for headers" {
					close(started)
				}
				<-r.Context().Done()
				close(stopped)
			}))
			defer srv.Close()
			transport := logCancellationTransport(func(r *http.Request) (*http.Response, error) {
				response, err := http.DefaultTransport.RoundTrip(r)
				if err == nil && phase == "reading body" {
					response.Body = &logReadNotifier{ReadCloser: response.Body, started: started}
				}
				return response, err
			})
			cli, err := client.New(client.WithHost(srv.URL), client.WithAPIVersion("1.51"), client.WithHTTPClient(&http.Client{Timeout: 2 * time.Second, Transport: transport}))
			require.NoError(t, err)
			defer cli.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := (&Environment{ID: "log-cancellation", client: cli}).ReadlogContext(ctx, 10)
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("Docker log request did not start")
			}
			cancel()
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(time.Second):
				t.Fatal("cancelled Docker log request remained blocked")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("Docker log response connection remained open")
			}
		})
	}
}
