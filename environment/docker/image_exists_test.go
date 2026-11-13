package docker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/docker/docker/client"
)

func TestImageExistsLocally(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantExists bool
		wantErr    bool
	}{
		{name: "present", status: http.StatusOK, body: `{}`, wantExists: true},
		{name: "missing", status: http.StatusNotFound, body: `{"message":"not found"}`},
		{name: "daemon error", status: http.StatusInternalServerError, body: `{"message":"broken"}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1.51/images/test-image/json" {
					t.Errorf("unexpected request path %q", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			cli, err := client.NewClientWithOpts(client.WithHost(srv.URL), client.WithVersion("1.51"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			got, err := ImageExistsLocally(context.Background(), cli, "test-image")
			if (err != nil) != tc.wantErr {
				t.Fatalf("ImageExistsLocally() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.wantExists {
				t.Fatalf("ImageExistsLocally() = %v, want %v", got, tc.wantExists)
			}
		})
	}
}
