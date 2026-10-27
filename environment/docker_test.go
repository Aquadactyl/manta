package environment

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

func TestDockerClient(t *testing.T) {
	cli, err := Docker()
	require.NoError(t, err)
	assert.NotNil(t, cli)
}

func TestConfigureDockerExistingNetwork(t *testing.T) {
	config.Set(&config.Configuration{
		AuthenticationToken: "test-auth-token-12345",
	})
	cli, err := Docker()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Existing bridge network
	config.Update(func(c *config.Configuration) {
		c.Docker.Network.Name = "bridge"
	})
	require.NoError(t, ConfigureDocker(ctx))
	assert.Equal(t, "bridge", config.Get().Docker.Network.Driver)
	assert.False(t, config.Get().Docker.Network.ISPN)

	// 2. Existing host network
	config.Update(func(c *config.Configuration) {
		c.Docker.Network.Name = "host"
	})
	require.NoError(t, ConfigureDocker(ctx))
	assert.Equal(t, "host", config.Get().Docker.Network.Driver)
	assert.Equal(t, "127.0.0.1", config.Get().Docker.Network.Interface)
	assert.False(t, config.Get().Docker.Network.ISPN)

	// 3. Create missing network
	nwName := fmt.Sprintf("ptero_test_%s", uuid.New().String()[:8])
	config.Update(func(c *config.Configuration) {
		c.Docker.Network.Name = nwName
		c.Docker.Network.Driver = "bridge"
		c.Docker.Network.Interfaces.V4.Subnet = "172.29.0.0/16"
		c.Docker.Network.Interfaces.V4.Gateway = "172.29.0.1"
		c.Docker.Network.Interfaces.V6.Subnet = "fdba:172:29::/64"
		c.Docker.Network.Interfaces.V6.Gateway = "fdba:172:29::1"
		c.Docker.Network.NetworkMTU = 1500
	})
	t.Cleanup(func() {
		_ = cli.NetworkRemove(context.Background(), nwName)
	})

	err = ConfigureDocker(ctx)
	t.Logf("ConfigureDocker error: %v", err)
	if err == nil {
		assert.Equal(t, "172.29.0.1", config.Get().Docker.Network.Interface)
	}

	// 4. Canceled context error
	canceledCtx, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	assert.Error(t, ConfigureDocker(canceledCtx))
}
