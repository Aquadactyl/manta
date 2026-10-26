package installer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

func TestValidationError(t *testing.T) {
	err := NewValidationError("invalid input")
	assert.Equal(t, "invalid input", err.Error())
	assert.True(t, IsValidationError(err))
	assert.False(t, IsValidationError(errors.New("other error")))

	// New with invalid UUID
	inst, err := New(context.Background(), server.NewEmptyManager(nil), ServerDetails{
		UUID: "not-a-uuid",
	})
	assert.Error(t, err)
	assert.True(t, IsValidationError(err))
	assert.Nil(t, inst)

	// Server() getter
	i := &Installer{StartOnCompletion: true}
	assert.Nil(t, i.Server())
	assert.True(t, i.StartOnCompletion)
}

func TestNew_RemoteErrors(t *testing.T) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	uuid := "11111111-2222-4333-8444-555555555555"

	t.Run("connection error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		client := remote.New("http://127.0.0.1:59999", remote.WithCredentials("id", "token"))
		m := server.NewEmptyManager(client)

		inst, err := New(ctx, m, ServerDetails{UUID: uuid})
		assert.Error(t, err)
		assert.Nil(t, inst)
	})

	t.Run("panel request error 404", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errors": []map[string]any{
					{
						"code":   "NotFound",
						"status": "404",
						"detail": "Server not found",
					},
				},
			})
		}))
		defer ts.Close()

		client := remote.New(ts.URL, remote.WithCredentials("id", "token"))
		m := server.NewEmptyManager(client)

		inst, err := New(context.Background(), m, ServerDetails{UUID: uuid})
		assert.Error(t, err)
		assert.Nil(t, inst)
		assert.Contains(t, err.Error(), "installer: could not get server configuration")
	})

	t.Run("init server failure", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(remote.ServerConfigurationResponse{
				Settings: json.RawMessage(`{"uuid":"different-uuid"}`),
			})
		}))
		defer ts.Close()

		client := remote.New(ts.URL, remote.WithCredentials("id", "token"))
		m := server.NewEmptyManager(client)

		inst, err := New(context.Background(), m, ServerDetails{UUID: uuid})
		assert.Error(t, err)
		assert.Nil(t, inst)
		assert.Contains(t, err.Error(), "installer: could not init server instance")
	})
}

