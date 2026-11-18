//go:build !darwin

package config

import "testing"

func TestOtherPlatformDefaults(t *testing.T) {
	if DefaultLocation != "/etc/aquadactyl/config.yml" {
		t.Fatalf("unexpected default configuration path: %s", DefaultLocation)
	}
	if locations := legacyConfigLocations(); len(locations) != 1 || locations[0] != "/etc/pterodactyl/config.yml" {
		t.Fatalf("unexpected legacy configuration locations: %v", locations)
	}
	c := &Configuration{}
	applyPlatformDefaults(c)
	if c.System.RootDirectory != "" {
		t.Fatalf("applyPlatformDefaults unexpectedly changed the configuration: %#v", c.System)
	}
	if handled, err := ensurePlatformUser(); err != nil || handled {
		t.Fatalf("ensurePlatformUser() = (%v, %v), want (false, nil)", handled, err)
	}
	if tz, ok := detectPlatformTimezone(); tz != "" || ok {
		t.Fatalf("detectPlatformTimezone() = (%q, %v), want empty and false", tz, ok)
	}
}
