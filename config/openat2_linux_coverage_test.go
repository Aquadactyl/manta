//go:build linux

package config

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestProbeOpenat2Success(t *testing.T) {
	oldOpenat2 := configOpenat2
	t.Cleanup(func() { configOpenat2 = oldOpenat2 })
	configOpenat2 = func(int, string, *unix.OpenHow) (int, error) { return -1, nil }
	if err := probeOpenat2(); err != nil {
		t.Fatal(err)
	}
}

func TestProbeOpenat2Error(t *testing.T) {
	oldOpenat2 := configOpenat2
	t.Cleanup(func() { configOpenat2 = oldOpenat2 })
	want := errors.New("openat2 rejected")
	configOpenat2 = func(int, string, *unix.OpenHow) (int, error) { return -1, want }
	if err := probeOpenat2(); !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}
