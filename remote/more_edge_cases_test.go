package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientRedirectHandling(t *testing.T) {
	// 1. Custom CheckRedirect via WithHttpClient
	customCalled := false
	customClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			customCalled = true
			return nil
		},
	}
	c := New("http://example.com", WithCredentials("id", "token"), WithHttpClient(customClient)).(*client)
	req, _ := http.NewRequest("GET", "http://example.com/redirect", nil)
	viaReq, _ := http.NewRequest("GET", "http://example.com/initial", nil)
	err := c.httpClient.CheckRedirect(req, []*http.Request{viaReq})
	require.NoError(t, err)
	assert.True(t, customCalled)

	// 2. Default redirect limit of 10
	cDefault := New("http://example.com", WithCredentials("id", "token")).(*client)
	var viaList []*http.Request
	for i := 0; i < 10; i++ {
		r, _ := http.NewRequest("GET", "http://example.com/path", nil)
		viaList = append(viaList, r)
	}
	err = cDefault.httpClient.CheckRedirect(req, viaList)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stopped after 10 redirects")
}

func TestClientRequestEdgeCases(t *testing.T) {
	c := New("http://example.com", WithCredentials("id", "token")).(*client)

	// Post with unmarshallable type (channel)
	_, err := c.Post(context.Background(), "/test", make(chan int))
	assert.Error(t, err)

	// requestOnce with invalid method
	_, err = c.requestOnce(context.Background(), "INVALID METHOD\x00", "/test", nil)
	assert.Error(t, err)
}

func TestResponseNilAndEmptyHandling(t *testing.T) {
	// Nil Response inside wrapper
	nilResp := &Response{Response: nil}
	assert.False(t, nilResp.HasError())
	_, err := nilResp.Read()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "attempting to read missing response")
	assert.Nil(t, nilResp.Error())

	// Response with nil Body
	emptyResp := &Response{Response: &http.Response{StatusCode: http.StatusOK, Body: nil}}
	b, err := emptyResp.Read()
	assert.NoError(t, err)
	assert.Nil(t, b)

	// Response with 200 OK calling Error() returns nil
	okResp := &Response{Response: &http.Response{StatusCode: http.StatusOK}}
	assert.Nil(t, okResp.Error())
}

func TestServersMalformedJSONResponses(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	defer s.Close()

	c := New(s.URL, WithCredentials("id", "token"), WithHttpClient(s.Client())).(*client)

	// ValidateSftpCredentials
	_, err := c.ValidateSftpCredentials(context.Background(), SftpAuthRequest{})
	assert.Error(t, err)

	// GetBackupRemoteUploadURLs
	_, err = c.GetBackupRemoteUploadURLs(context.Background(), "backup-1", 1024)
	assert.Error(t, err)

	// getServersPaged
	_, _, err = c.getServersPaged(context.Background(), 1, 10)
	assert.Error(t, err)

	// GetServers with canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.GetServers(ctx, 1)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}
