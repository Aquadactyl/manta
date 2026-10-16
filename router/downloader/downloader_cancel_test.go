package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDownloadCanceledBeforeExecuteMakesNoRequest(t *testing.T) {
	s := newPullTestServer(t)
	var requests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("payload"))
	}))
	t.Cleanup(origin.Close)
	u, err := url.Parse(origin.URL + "/file")
	require.NoError(t, err)
	dl := New(s, DownloadRequest{Directory: "/", URL: u, FileName: "file"})
	dl.Cancel()
	require.ErrorIs(t, dl.Execute(), context.Canceled)
	require.Zero(t, requests.Load())
	require.Nil(t, ByID(dl.Identifier))
	require.Empty(t, ByServer(s.ID()))
}

func TestConcurrentDownloadCancellationStopsRequest(t *testing.T) {
	s := newPullTestServer(t)
	started := make(chan struct{})
	requestCanceled := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		w.Header().Set("Content-Disposition", `attachment; filename="named.bin"`)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(requestCanceled)
	}))
	t.Cleanup(origin.Close)
	u, err := url.Parse(origin.URL + "/file")
	require.NoError(t, err)
	dl := New(s, DownloadRequest{Directory: "/", URL: u, UseHeader: true})
	t.Cleanup(dl.Cancel)
	done := make(chan error, 1)
	go func() { done <- dl.Execute() }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = dl.Path()
			_ = dl.Progress()
			dl.Cancel()
		}()
	}
	wg.Wait()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("download did not stop")
	}
	select {
	case <-requestCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("origin request was not canceled")
	}
	require.Nil(t, ByID(dl.Identifier))
	instance.mu.RLock()
	_, tracked := instance.serverCache[s.ID()]
	instance.mu.RUnlock()
	require.False(t, tracked)
}
