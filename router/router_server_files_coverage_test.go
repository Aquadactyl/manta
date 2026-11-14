package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/system"
)

func TestFileBatchContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	if err := fileBatchContextError(ctx); err != nil {
		t.Fatalf("unexpected error for active context: %v", err)
	}
	cancel()
	if err := fileBatchContextError(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context, got %v", err)
	}
	called := false
	if err := runFileBatch(ctx, func() error { called = true; return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled batch error, got %v", err)
	}
	if called {
		t.Fatal("batch callback ran after cancellation")
	}
	if err := runFileBatch(context.Background(), func() error { return nil }); err != nil {
		t.Fatalf("unexpected active batch error: %v", err)
	}
}

func TestDeleteServerLogsFilesystemRemovalFailure(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	previous := removeServerFiles
	called := make(chan struct{}, 1)
	removeServerFiles = func(string) error {
		called <- struct{}{}
		return errors.New("remove failed")
	}
	t.Cleanup(func() { removeServerFiles = previous })

	req := httptest.NewRequest(http.MethodDelete, "/api/servers/"+s.ID(), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("server file removal was not attempted")
	}
}

func TestPostServerPowerIgnoresAlreadyRunning(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	env := transferTestEnvironment{st: system.NewAtomicString(environment.ProcessRunningState)}
	s.Environment = env
	req := httptest.NewRequest(http.MethodPost, "/api/servers/"+s.ID()+"/power", strings.NewReader(`{"action":"start"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	time.Sleep(20 * time.Millisecond)
}
