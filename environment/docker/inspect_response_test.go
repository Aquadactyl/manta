package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
)

func TestFastInspectClosesResponseWhenRedirectFails(t *testing.T) {
	previousSettings, previousEnabled := cli, fastEnabled
	t.Cleanup(func() { o = sync.Once{}; cli, fastEnabled = previousSettings, previousEnabled })
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer ts.Close()
	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("redirect rejected")
	}}
	c, err := client.NewClientWithOpts(client.WithHost(ts.URL), client.WithVersion("1.51"), client.WithHTTPClient(httpClient))
	require.NoError(t, err)
	defer c.Close()
	config.Set(&config.Configuration{AuthenticationToken: "test-token", Docker: config.DockerConfiguration{UsePerformantInspect: true}})
	o = sync.Once{}

	_, err = (&Environment{Id: "inspect-redirect", client: c}).ContainerInspect(context.Background())
	require.ErrorContains(t, err, "redirect rejected")
}

func TestFastInspectRejectsMalformedAndTruncatedResponses(t *testing.T) {
	previousSettings, previousEnabled := cli, fastEnabled
	t.Cleanup(func() {
		o = sync.Once{}
		cli, fastEnabled = previousSettings, previousEnabled
	})
	for _, test := range []struct {
		name      string
		body      string
		truncated bool
		want      string
	}{
		{name: "malformed JSON", body: `{"State":`, want: "unexpected end"},
		{name: "invalid field type", body: `{"State":{"Running":"yes"}}`, want: "cannot unmarshal"},
		{name: "truncated body", body: `{}`, truncated: true, want: "failed to read response body"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.truncated {
					w.Header().Set("Content-Length", "100")
				}
				_, _ = io.WriteString(w, test.body)
			}))
			defer ts.Close()
			c, err := client.NewClientWithOpts(client.WithHost(ts.URL), client.WithVersion("1.51"))
			require.NoError(t, err)
			defer c.Close()
			config.Set(&config.Configuration{AuthenticationToken: "test-token", Docker: config.DockerConfiguration{UsePerformantInspect: true}})
			o = sync.Once{}
			e := &Environment{Id: "inspect-response", client: c}
			_, err = e.ContainerInspect(context.Background())
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestReadlogReturnsScannerErrorForOversizedLine(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 70*1024)
	frame := make([]byte, 8+len(payload))
	frame[0] = 1 // Docker stdout frame.
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(frame)
	}))
	defer ts.Close()
	c, err := client.NewClientWithOpts(client.WithHost(ts.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer c.Close()

	_, err = (&Environment{Id: "oversized-log", client: c}).Readlog(10)
	require.ErrorContains(t, err, "token too long")
}

func TestExistsAndExitStateReturnUnexpectedInspectErrors(t *testing.T) {
	previousSettings, previousEnabled := cli, fastEnabled
	t.Cleanup(func() {
		o = sync.Once{}
		cli, fastEnabled = previousSettings, previousEnabled
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"daemon unavailable"}`, http.StatusInternalServerError)
	}))
	defer ts.Close()
	c, err := client.NewClientWithOpts(client.WithHost(ts.URL), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer c.Close()
	config.Set(&config.Configuration{AuthenticationToken: "test-token", Docker: config.DockerConfiguration{UsePerformantInspect: false}})
	o = sync.Once{}
	e := &Environment{Id: "inspect-failure", client: c}

	exists, err := e.Exists()
	require.Error(t, err)
	require.False(t, exists)
	_, _, err = e.ExitState()
	require.ErrorContains(t, err, "environment/docker: failed to inspect container")
	require.ErrorContains(t, e.InSituUpdate(), "environment/docker: could not inspect container")
}

func TestFastInspectRejectsInvalidRequestPath(t *testing.T) {
	previousSettings, previousEnabled := cli, fastEnabled
	t.Cleanup(func() { o = sync.Once{}; cli, fastEnabled = previousSettings, previousEnabled })
	c, err := client.NewClientWithOpts(client.WithHost("http://127.0.0.1:1"), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer c.Close()
	config.Set(&config.Configuration{AuthenticationToken: "test-token", Docker: config.DockerConfiguration{UsePerformantInspect: true}})
	o = sync.Once{}
	_, err = (&Environment{Id: "invalid\nidentifier", client: c}).ContainerInspect(context.Background())
	require.ErrorContains(t, err, "invalid control character")
}

func TestUptimeRejectsMalformedStartTimeAndReturnsZeroWhenStopped(t *testing.T) {
	previousSettings, previousEnabled := cli, fastEnabled
	t.Cleanup(func() { o = sync.Once{}; cli, fastEnabled = previousSettings, previousEnabled })
	for _, test := range []struct {
		body      string
		wantError bool
	}{
		{body: `{"State":{"Running":true,"StartedAt":"invalid"}}`, wantError: true},
		{body: `{"State":{"Running":false}}`},
	} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.Copy(w, strings.NewReader(test.body)) }))
		c, err := client.NewClientWithOpts(client.WithHost(ts.URL), client.WithVersion("1.51"))
		require.NoError(t, err)
		config.Set(&config.Configuration{AuthenticationToken: "test-token", Docker: config.DockerConfiguration{UsePerformantInspect: true}})
		o = sync.Once{}
		e := &Environment{Id: "uptime-response", client: c, Configuration: environment.NewConfiguration(environment.Settings{}, nil)}
		uptime, err := e.Uptime(context.Background())
		if test.wantError {
			require.ErrorContains(t, err, "failed to parse container start time")
		} else {
			require.NoError(t, err)
		}
		require.Zero(t, uptime)
		require.NoError(t, c.Close())
		ts.Close()
	}
}
