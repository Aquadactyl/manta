package docker

import (
	"errors"
	"testing"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/environment"
)

func TestNewReturnsDockerClientFactoryError(t *testing.T) {
	want := errors.New("docker client unavailable")
	previous := dockerClientForEnvironment
	dockerClientForEnvironment = func() (*client.Client, error) { return nil, want }
	t.Cleanup(func() { dockerClientForEnvironment = previous })

	got, err := New("test-server", &Metadata{Image: "alpine:latest"}, environment.NewConfiguration(environment.Settings{}, nil))
	require.Nil(t, got)
	require.ErrorIs(t, err, want)
}
