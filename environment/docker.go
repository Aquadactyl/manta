package environment

import (
	"context"
	"net/netip"
	"strconv"
	"sync"

	"emperror.dev/errors"
	"github.com/apex/log"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Aquadactyl/manta/config"
	"github.com/Aquadactyl/manta/system"
)

var (
	_conce              sync.Once
	_client             *client.Client
	dockerClientFactory = Docker
)

// Docker returns a docker client to be used throughout the codebase. Once a
// client has been created it will be returned for all subsequent calls to this
// function.
func Docker() (*client.Client, error) {
	var err error
	_conce.Do(func() {
		_client, err = client.NewClientWithOpts(system.DockerClientOpts()...)
	})
	return _client, errors.Wrap(err, "environment/docker: could not create client")
}

// ConfigureDocker configures the required network for the docker environment.
func ConfigureDocker(ctx context.Context) error {
	// Ensure the required docker network exists on the system.
	cli, err := dockerClientFactory()
	if err != nil {
		return err
	}

	nw := config.Get().Docker.Network
	resource, err := cli.NetworkInspect(ctx, nw.Name, client.NetworkInspectOptions{})
	if err != nil {
		if !cerrdefs.IsNotFound(err) {
			return err
		}

		log.Info("creating missing pterodactyl0 interface, this could take a few seconds...")
		if err := createDockerNetwork(ctx, cli); err != nil {
			return err
		}
	}

	config.Update(func(c *config.Configuration) {
		c.Docker.Network.Driver = resource.Network.Driver
		switch c.Docker.Network.Driver {
		case "host":
			c.Docker.Network.Interface = "127.0.0.1"
			c.Docker.Network.ISPN = false
		case "overlay":
			fallthrough
		case "weavemesh":
			c.Docker.Network.Interface = ""
			c.Docker.Network.ISPN = true
		default:
			c.Docker.Network.ISPN = false
		}
	})
	return nil
}

// Creates a new network on the machine if one does not exist already.
func createDockerNetwork(ctx context.Context, cli *client.Client) error {
	nw := config.Get().Docker.Network
	enableIPv6 := nw.Interfaces.V6.Enabled
	ipamConfigV4, err := networkIPAMConfig(nw.Interfaces.V4.Subnet, nw.Interfaces.V4.Gateway)
	if err != nil {
		return errors.Wrap(err, "environment/docker: invalid IPv4 network configuration")
	}
	ipamConfigV6, err := networkIPAMConfig(nw.Interfaces.V6.Subnet, nw.Interfaces.V6.Gateway)
	if err != nil {
		return errors.Wrap(err, "environment/docker: invalid IPv6 network configuration")
	}
	_, err = cli.NetworkCreate(ctx, nw.Name, client.NetworkCreateOptions{
		Driver:     nw.Driver,
		EnableIPv6: &enableIPv6,
		Internal:   nw.IsInternal,
		IPAM: &network.IPAM{
			Config: []network.IPAMConfig{ipamConfigV4, ipamConfigV6},
		},
		Options: map[string]string{
			"encryption": "false",
			"com.docker.network.bridge.default_bridge":       "false",
			"com.docker.network.bridge.enable_icc":           strconv.FormatBool(nw.EnableICC),
			"com.docker.network.bridge.enable_ip_masquerade": "true",
			"com.docker.network.bridge.host_binding_ipv4":    "0.0.0.0",
			"com.docker.network.bridge.name":                 "pterodactyl0",
			"com.docker.network.driver.mtu":                  strconv.FormatInt(nw.NetworkMTU, 10),
		},
	})
	if err != nil {
		return err
	}
	if nw.Driver != "host" && nw.Driver != "overlay" && nw.Driver != "weavemesh" {
		config.Update(func(c *config.Configuration) {
			c.Docker.Network.Interface = c.Docker.Network.Interfaces.V4.Gateway
		})
	}
	return nil
}

func networkIPAMConfig(subnet, gateway string) (network.IPAMConfig, error) {
	var config network.IPAMConfig
	if subnet != "" {
		prefix, err := netip.ParsePrefix(subnet)
		if err != nil {
			return config, err
		}
		config.Subnet = prefix
	}
	if gateway != "" {
		address, err := netip.ParseAddr(gateway)
		if err != nil {
			return config, err
		}
		config.Gateway = address
	}
	return config, nil
}
