//go:build !darwin

package config

import "testing"

func TestOtherPlatformDefaults(t *testing.T) {
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
