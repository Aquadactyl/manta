package environment

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

func dockerMountCheckerForTest(t *testing.T, handler http.Handler) *dockerMountChecker {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cli, err := client.NewClientWithOpts(client.WithHost(srv.URL), client.WithVersion("1.51"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return &dockerMountChecker{cli: cli}
}

func TestDockerMountCheckerHasImage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		want    bool
		wantErr bool
	}{
		{name: "exists", status: http.StatusOK, body: `{}`, want: true},
		{name: "not found", status: http.StatusNotFound, body: `{"message":"missing"}`},
		{name: "daemon failure", status: http.StatusInternalServerError, body: `{"message":"broken"}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker := dockerMountCheckerForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1.51/images/example/json" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			got, err := checker.HasImage(context.Background(), "example")
			if (err != nil) != tc.wantErr {
				t.Fatalf("HasImage() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("HasImage() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDockerMountCheckerPullImage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success", status: http.StatusOK},
		{name: "request error", status: http.StatusInternalServerError, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker := dockerMountCheckerForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1.51/images/create" || r.URL.Query().Get("fromImage") != "ghcr.io/pterodactyl/installers" || r.URL.Query().Get("tag") != "alpine" {
					t.Errorf("unexpected pull request: %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"status":"ok"}`)
			}))
			err := checker.PullImage(context.Background(), mountCheckImage)
			if (err != nil) != tc.wantErr {
				t.Fatalf("PullImage() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestDockerMountCheckerRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failAt   string
		waitBody string
		wantCode int64
		wantErr  string
	}{
		{name: "success", waitBody: `{"StatusCode":0}`},
		{name: "container exit status", waitBody: `{"StatusCode":7}`, wantCode: 7},
		{name: "wait reports error", waitBody: `{"Error":{"Message":"wait failed"}}`, wantErr: "wait failed"},
		{name: "create fails", failAt: "create", wantErr: "create failed"},
		{name: "start fails", failAt: "start", wantErr: "start failed"},
		{name: "wait request fails", failAt: "wait", wantErr: "wait failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			removed := false
			checker := dockerMountCheckerForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/v1.51")
				switch {
				case r.Method == http.MethodPost && path == "/containers/create":
					if tc.failAt == "create" {
						http.Error(w, `{"message":"create failed"}`, http.StatusInternalServerError)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"Id":"mount-check","Warnings":[]}`)
				case r.Method == http.MethodPost && path == "/containers/mount-check/start":
					if tc.failAt == "start" {
						http.Error(w, `{"message":"start failed"}`, http.StatusInternalServerError)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodPost && path == "/containers/mount-check/wait":
					if tc.failAt == "wait" {
						http.Error(w, `{"message":"wait failed"}`, http.StatusInternalServerError)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, tc.waitBody)
				case r.Method == http.MethodDelete && path == "/containers/mount-check":
					removed = true
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected Docker request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			code, err := checker.Run(context.Background(), &container.Config{Image: mountCheckImage}, &container.HostConfig{})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Run() error = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if code != tc.wantCode {
				t.Fatalf("Run() code = %d, want %d", code, tc.wantCode)
			}
			if tc.failAt != "create" && !removed {
				t.Fatal("created container was not removed")
			}
		})
	}
}

func TestDockerMountCheckerRunHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	checker := dockerMountCheckerForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1.51")
		switch {
		case r.Method == http.MethodPost && path == "/containers/create":
			_, _ = fmt.Fprint(w, `{"Id":"mount-check","Warnings":[]}`)
		case r.Method == http.MethodPost && path == "/containers/mount-check/start":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && path == "/containers/mount-check/wait":
			close(started)
			<-r.Context().Done()
		case r.Method == http.MethodDelete && path == "/containers/mount-check":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := checker.Run(ctx, &container.Config{}, &container.HostConfig{}); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("wait request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run() returned nil after cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after cancellation")
	}
}
