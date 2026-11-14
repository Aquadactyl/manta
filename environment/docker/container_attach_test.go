package docker

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/system"
	"github.com/stretchr/testify/require"
)

type attachTestReader struct{ err error }

func (r attachTestReader) Read([]byte) (int, error) { return 0, r.err }

func TestAttachReturnsDockerAttachError(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.51/containers/container-id/attach" {
			t.Errorf("unexpected Docker API path: %s", r.URL.Path)
		}
		http.Error(w, `{"message":"attach failed"}`, http.StatusInternalServerError)
	}))
	defer api.Close()

	dockerClient, err := client.NewClientWithOpts(client.WithHost("tcp://"+api.Listener.Addr().String()), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer dockerClient.Close()

	env := &Environment{Id: "container-id", client: dockerClient}
	err = env.Attach(context.Background())
	require.Error(t, err)
	require.False(t, env.IsAttached())
}

func TestAttachDoesNothingWhenAlreadyAttached(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	env := &Environment{}
	env.SetStream(&types.HijackedResponse{Conn: clientSide, Reader: bufio.NewReader(clientSide)})

	require.NoError(t, env.Attach(context.Background()))
	require.True(t, env.IsAttached())
}

func TestAttachConsumesOutputAndClearsStoppedStream(t *testing.T) {
	config.Set(&config.Configuration{})
	oldAttach := dockerContainerAttach
	t.Cleanup(func() { dockerContainerAttach = oldAttach })
	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	dockerContainerAttach = func(context.Context, *client.Client, string, container.AttachOptions) (types.HijackedResponse, error) {
		return types.HijackedResponse{Conn: clientSide, Reader: bufio.NewReader(clientSide)}, nil
	}

	cli, err := client.NewClientWithOpts(client.WithHost("tcp://127.0.0.1:1"), client.WithVersion("1.51"))
	require.NoError(t, err)
	defer cli.Close()
	env := &Environment{
		Id:      "container-id",
		client:  cli,
		st:      system.NewAtomicString(environment.ProcessRunningState),
		emitter: events.NewBus(),
	}
	output := make(chan string, 1)
	env.SetLogCallback(func(line []byte) { output <- string(line) })

	go func() {
		_, _ = serverSide.Write([]byte("server output\n"))
		_ = serverSide.Close()
	}()
	require.NoError(t, env.Attach(context.Background()))

	select {
	case line := <-output:
		require.Equal(t, "server output", line)
	case <-time.After(time.Second):
		t.Fatal("attach did not forward output")
	}
	deadline := time.Now().Add(time.Second)
	for (env.IsAttached() || env.State() != environment.ProcessOfflineState) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	require.False(t, env.IsAttached())
	require.Equal(t, environment.ProcessOfflineState, env.State())
}

func TestAttachScannerErrorKeepsStreamWhenReattachSucceeds(t *testing.T) {
	oldAttach, oldPoll, oldReattach := dockerContainerAttach, pollContainerResources, reattachAfterStreamEnd
	t.Cleanup(func() {
		dockerContainerAttach, pollContainerResources, reattachAfterStreamEnd = oldAttach, oldPoll, oldReattach
	})
	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() { _ = clientSide.Close(); _ = serverSide.Close() })
	want := errors.New("scanner read failed")
	dockerContainerAttach = func(context.Context, *client.Client, string, container.AttachOptions) (types.HijackedResponse, error) {
		return types.HijackedResponse{Conn: clientSide, Reader: bufio.NewReader(attachTestReader{err: want})}, nil
	}
	pollStarted := make(chan struct{})
	pollContainerResources = func(_ *Environment, ctx context.Context) error {
		close(pollStarted)
		<-ctx.Done()
		return ctx.Err()
	}
	reattachCalled := make(chan struct{})
	reattachAfterStreamEnd = func(*Environment) bool {
		close(reattachCalled)
		return true
	}
	env := &Environment{Id: "container-id", st: system.NewAtomicString(environment.ProcessRunningState)}
	require.NoError(t, env.Attach(context.Background()))
	select {
	case <-pollStarted:
	case <-time.After(time.Second):
		t.Fatal("resource poller did not start")
	}

	select {
	case <-reattachCalled:
	case <-time.After(time.Second):
		t.Fatal("attach did not handle the scanner error")
	}
	require.False(t, env.IsAttached())
	require.Equal(t, environment.ProcessRunningState, env.State())
}

func TestReportAttachPollError(t *testing.T) {
	env := &Environment{Id: "container-id"}
	reportAttachPollError(env, errors.New("poll failed"))
	reportAttachPollError(env, io.EOF)
	reportAttachPollError(env, context.Canceled)
}
