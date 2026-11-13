package config

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistryConfigurationBase64MarshalError(t *testing.T) {
	oldMarshal := marshalRegistryAuth
	t.Cleanup(func() { marshalRegistryAuth = oldMarshal })
	want := errors.New("auth marshal failed")
	marshalRegistryAuth = func(any) ([]byte, error) { return nil, want }

	_, err := (RegistryConfiguration{Username: "user", Password: "password"}).Base64()
	require.ErrorIs(t, err, want)
}

func TestParseDockerRegistryReferenceDockerHubSingleComponent(t *testing.T) {
	domain, registryPath, ok := parseDockerRegistryReference("docker.io/v1")
	require.True(t, ok)
	require.Equal(t, "docker.io", domain)
	require.Equal(t, "library/v1", registryPath)
}
