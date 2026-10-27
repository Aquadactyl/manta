package cron

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

func TestScheduler(t *testing.T) {
	setupDatabase(t)
	defer o.Store(false)

	// Reset state
	o.Store(false)

	// Test invalid timezone
	cfg := config.Get()
	origTz := cfg.System.Timezone
	cfg.System.Timezone = "Invalid/Timezone"
	config.Set(cfg)

	s, err := Scheduler(context.Background(), server.NewEmptyManager(nil))
	assert.Error(t, err)
	assert.Nil(t, s)

	// Reset singleton
	o.Store(false)

	// Test valid timezone
	cfg.System.Timezone = "UTC"
	cfg.System.ActivitySendCount = 100
	cfg.System.ActivitySendInterval = 60
	config.Set(cfg)

	s, err = Scheduler(context.Background(), server.NewEmptyManager(nil))
	assert.NoError(t, err)
	assert.NotNil(t, s)
	defer s.Clear()

	// Run scheduled jobs
	_ = s.RunByTag("activity")
	_ = s.RunByTag("sftp")

	// Test ErrCronRunning branch for activityCron
	ac := &activityCron{mu: system.NewAtomicBool(true)}
	assert.ErrorIs(t, ac.Run(context.Background()), ErrCronRunning)

	// Test ErrCronRunning branch for sftpCron
	sc := &sftpCron{mu: system.NewAtomicBool(true)}
	assert.ErrorIs(t, sc.Run(context.Background()), ErrCronRunning)

	// Test batchSize fallback
	acEmpty := &activityCron{max: 0}
	assert.Equal(t, 100, acEmpty.batchSize())

	// Calling again should fail
	s2, err2 := Scheduler(context.Background(), server.NewEmptyManager(nil))
	assert.Error(t, err2)
	assert.Nil(t, s2)

	cfg.System.Timezone = origTz
	config.Set(cfg)
}

func TestCronEdgeCasesAndDeliveryErrors(t *testing.T) {
	setupDatabase(t)
	defer o.Store(false)

	// 1. deliver with empty slice
	ids, err := deliver(context.Background(), nil, nil)
	assert.NoError(t, err)
	assert.Nil(t, ids)

	// 2. isRefused with non-RequestError and with 403 RequestError (no 5xx retry loop)
	assert.False(t, isRefused(errors.New("plain error")))
	srv403 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"code":"Forbidden","status":"403","detail":"err"}]}`))
	}))
	defer srv403.Close()

	cli403 := remote.New(srv403.URL)
	err403 := cli403.SendActivityLogs(context.Background(), []models.Activity{{Server: testServerUuid}})
	assert.Error(t, err403)
	assert.False(t, isRefused(err403))

	// 3. deliver when first call returns 400 and split half returns 403
	calls := 0
	srvSplitFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"code":"BadRequest","status":"400","detail":"bad"}]}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"code":"Forbidden","status":"403","detail":"err"}]}`))
	}))
	defer srvSplitFail.Close()

	_, err = deliver(context.Background(), remote.New(srvSplitFail.URL), []batchItem{
		{activity: models.Activity{Server: testServerUuid, Event: "e1"}, ids: []int{1}},
		{activity: models.Activity{Server: testServerUuid, Event: "e2"}, ids: []int{2}},
	})
	assert.Error(t, err)

	// 4. activityCron & sftpCron with empty DB, delivery error, and canceled context
	mgr403 := server.NewEmptyManager(cli403)
	ac := &activityCron{mu: system.NewAtomicBool(false), manager: mgr403, max: 1}
	sc := &sftpCron{mu: system.NewAtomicBool(false), manager: mgr403, max: 0}

	// Empty DB run
	assert.NoError(t, ac.Run(context.Background()))
	assert.NoError(t, sc.Run(context.Background()))

	// Delivery error in activityCron
	insertActivity(t, "server:console.command", validIP, "cmd")
	assert.Error(t, ac.Run(context.Background()))
	setupDatabase(t)

	// Delivery error in sftpCron
	insertActivity(t, "server:sftp.write", validIP, "/a.txt")
	assert.Error(t, sc.Run(context.Background()))
	setupDatabase(t)

	// Canceled context in pruneActivity, deleteActivity, activityCron.sendBatch, sftpCron.Run
	ctxCancel, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, pruneActivity(ctxCancel, 10))
	assert.Error(t, deleteActivity(ctxCancel, []int{1, 2}))
	_, err = ac.sendBatch(ctxCancel)
	assert.Error(t, err)
	assert.Error(t, ac.Run(ctxCancel))
	assert.Error(t, sc.Run(ctxCancel))

	// 5. eventMap.Push edge cases: max events limit reached (forActivity returns nil) & non-slice files metadata
	em := &eventMap{max: 1, m: map[mapKey]*eventGroup{}}
	tNow := time.Now()
	a1 := models.Activity{
		Server:    testServerUuid,
		Event:     "server:sftp.write",
		IP:        validIP,
		Timestamp: tNow,
		Metadata:  models.ActivityMeta{"files": "not-a-slice"},
	}
	assert.True(t, em.Push(a1))

	// Earlier timestamp updates group timestamp
	a1Earlier := models.Activity{
		Server:    testServerUuid,
		Event:     "server:sftp.write",
		IP:        validIP,
		Timestamp: tNow.Add(-10 * time.Second),
		Metadata:  models.ActivityMeta{"files": []any{"/b.txt"}},
	}
	assert.True(t, em.Push(a1Earlier))

	// Different event when len(em.m) >= em.max returns false
	a2 := models.Activity{
		Server:    testServerUuid,
		Event:     "server:sftp.delete",
		IP:        validIP,
		Timestamp: tNow,
	}
	assert.False(t, em.Push(a2))

	// 6. Execute Scheduler job functions directly (normal & error paths)
	o.Store(false)
	cfg := config.Get()
	cfg.System.Timezone = "UTC"
	cfg.System.ActivitySendCount = 10
	cfg.System.ActivitySendInterval = 1
	config.Set(cfg)

	sched, err := Scheduler(ctxCancel, mgr403)
	assert.NoError(t, err)
	sched.StartAsync()
	time.Sleep(150 * time.Millisecond)
	sched.Stop()
}
