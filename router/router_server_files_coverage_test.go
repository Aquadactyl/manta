package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/transfer"
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

func TestPostTransfersRegistrationAndSetupFailures(t *testing.T) {
	t.Run("installer failure and panel status failure", func(t *testing.T) {
		panel := &transferTestPanel{configErr: errors.New("configuration unavailable"), statusErr: errors.New("status unavailable")}
		h := newTransferHarness(t, panel)
		id := "a1000000-0000-4000-8000-000000000001"
		w := h.postTransfer(bytes.NewBuffer(nil), "multipart/form-data", signTransferTestToken(t, id))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("manager registration conflict", func(t *testing.T) {
		h := newTransferHarness(t, &transferTestPanel{})
		id := "a1000000-0000-4000-8000-000000000002"
		settings, err := json.Marshal(map[string]string{"uuid": id})
		if err != nil {
			t.Fatal(err)
		}
		h.panel.config.Settings = settings
		previousCreate := createTransferEnvironment
		createTransferEnvironment = func(*server.Server) error { return nil }
		t.Cleanup(func() { createTransferEnvironment = previousCreate })
		previousAdd := addTransferServer
		addTransferServer = func(manager *server.Manager, s *server.Server) bool { return false }
		t.Cleanup(func() { addTransferServer = previousAdd })
		w := h.postTransfer(bytes.NewBuffer(nil), "multipart/form-data", signTransferTestToken(t, id))
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("incoming registration conflict", func(t *testing.T) {
		h := newTransferHarness(t, &transferTestPanel{})
		id := "a1000000-0000-4000-8000-000000000003"
		settings, err := json.Marshal(map[string]string{"uuid": id})
		if err != nil {
			t.Fatal(err)
		}
		h.panel.config.Settings = settings
		previousCreate := createTransferEnvironment
		createTransferEnvironment = func(*server.Server) error { return nil }
		t.Cleanup(func() { createTransferEnvironment = previousCreate })
		previousAdd := addIncomingTransfer
		addIncomingTransfer = func(*transfer.Transfer) bool { return false }
		t.Cleanup(func() { addIncomingTransfer = previousAdd })
		w := h.postTransfer(bytes.NewBuffer(nil), "multipart/form-data", signTransferTestToken(t, id))
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("archive data directory failure", func(t *testing.T) {
		h := newTransferHarness(t, &transferTestPanel{})
		id := "a1000000-0000-4000-8000-000000000004"
		settings, err := json.Marshal(map[string]string{"uuid": id})
		if err != nil {
			t.Fatal(err)
		}
		h.panel.config.Settings = settings
		previousCreate := createTransferEnvironment
		createTransferEnvironment = func(*server.Server) error { return nil }
		t.Cleanup(func() { createTransferEnvironment = previousCreate })
		previousEnsure := ensureTransferDataDirectory
		ensureTransferDataDirectory = func(*server.Server) error { return errors.New("directory unavailable") }
		t.Cleanup(func() { ensureTransferDataDirectory = previousEnsure })
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("archive", "archive.tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte("archive"))
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		w := h.postTransfer(&body, writer.FormDataContentType(), signTransferTestToken(t, id))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
		}
	})
}
