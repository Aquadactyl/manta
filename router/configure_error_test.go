package router

import (
	"testing"

	"github.com/Aquadactyl/manta/config"
)

func TestConfigurePanicsOnInvalidTrustedProxy(t *testing.T) {
	old := config.Get()
	t.Cleanup(func() { config.Set(old) })
	config.Set(&config.Configuration{API: config.APIConfiguration{TrustedProxies: []string{"invalid"}}})

	defer func() {
		if recover() == nil {
			t.Fatal("expected invalid trusted proxies to panic")
		}
	}()
	Configure(nil, nil)
}
