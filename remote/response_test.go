package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type trackedResponseBody struct {
	io.Reader
	closed bool
	read   int64
}

func (b *trackedResponseBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += int64(n)
	return n, err
}

func (b *trackedResponseBody) Close() error {
	b.closed = true
	return nil
}

type failingResponseReader struct{}

func (failingResponseReader) Read(p []byte) (int, error) {
	return copy(p, `{"ok":true}`), io.ErrUnexpectedEOF
}

func TestResponseReadRejectsOversizedBodies(t *testing.T) {
	for _, contentLength := range []int64{MaxResponseSize + 1, -1} {
		body := &trackedResponseBody{Reader: strings.NewReader(strings.Repeat("a", int(MaxResponseSize)+2))}
		res := Response{&http.Response{Body: body, ContentLength: contentLength}}
		_, err := res.Read()
		require.ErrorIs(t, err, ErrResponseTooLarge)
		require.True(t, body.closed)
		require.LessOrEqual(t, body.read, MaxResponseSize+1)
		if contentLength > 0 {
			require.Zero(t, body.read)
		}
	}
}

func TestResponseBindJSONRejectsReadErrors(t *testing.T) {
	body := &trackedResponseBody{Reader: failingResponseReader{}}
	res := Response{&http.Response{Body: body, ContentLength: -1}}
	var data map[string]bool
	require.ErrorIs(t, res.BindJSON(&data), io.ErrUnexpectedEOF)
	require.Nil(t, data)
	require.True(t, body.closed)
}

func TestResponseReadClosesOriginalAndRemainsReadable(t *testing.T) {
	body := &trackedResponseBody{Reader: strings.NewReader(`{"ok":true}`)}
	res := Response{&http.Response{Body: body}}
	first, err := res.Read()
	require.NoError(t, err)
	require.True(t, body.closed)
	second, err := res.Read()
	require.NoError(t, err)
	require.Equal(t, first, second)
	var data map[string]bool
	require.NoError(t, res.BindJSON(&data))
	require.True(t, data["ok"])
}

func TestRequestRetriesCompleteBody(t *testing.T) {
	var received []string
	c, srv := createTestClient(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		received = append(received, string(body))
		if len(received) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	t.Cleanup(srv.Close)
	res, err := c.request(context.Background(), http.MethodPost, "/test", bytes.NewBufferString(`{"hello":"world"}`), func(r *http.Request) {
		require.NotNil(t, r.GetBody)
	})
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, []string{`{"hello":"world"}`, `{"hello":"world"}`}, received)
}

func TestCanceledRequestDoesNotRetry(t *testing.T) {
	c, srv := createTestClient(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Get(ctx, "/test", nil)
	require.True(t, errors.Is(err, context.Canceled))
}

func TestPanelRedirectCannotForwardCredentialsOrBodyToAnotherOrigin(t *testing.T) {
	var targetRequests, originRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
		http.Redirect(w, r, target.URL+"/collect", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(origin.Close)
	httpClient := origin.Client()
	c := New(origin.URL, WithCredentials("id", "secret"), WithHttpClient(httpClient)).(*client)
	_, err := c.Post(context.Background(), "/sftp/auth", map[string]string{"password": "secret-password"})
	require.Error(t, err)
	require.Equal(t, http.StatusTemporaryRedirect, AsRequestError(err).StatusCode())
	require.Equal(t, int32(1), originRequests.Load())
	require.Zero(t, targetRequests.Load())
	require.Nil(t, httpClient.CheckRedirect, "caller-provided clients must not be mutated")
}

func TestPanelSameOriginRedirectRetainsAuthenticationAndBody(t *testing.T) {
	var authorization, body string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/remote/final" {
			http.Redirect(w, r, "/api/remote/final", http.StatusTemporaryRedirect)
			return
		}
		authorization = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(origin.Close)
	c := New(origin.URL, WithCredentials("id", "secret")).(*client)
	res, err := c.Post(context.Background(), "/test", map[string]string{"hello": "world"})
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, "Bearer id.secret", authorization)
	require.JSONEq(t, `{"hello":"world"}`, body)
}
