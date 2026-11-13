//go:build !darwin

package system

import (
	"errors"
	"testing"

	dockersystem "github.com/docker/docker/api/types/system"
)

func TestHostOSNameUsesReleaseFallbacks(t *testing.T) {
	previous := readOSRelease
	t.Cleanup(func() { readOSRelease = previous })

	cases := []struct {
		name     string
		release  map[string]string
		err      error
		fallback string
		want     string
	}{
		{name: "pretty name", release: map[string]string{"PRETTY_NAME": "Linux Pretty", "NAME": "Linux"}, fallback: "Docker OS", want: "Linux Pretty"},
		{name: "name", release: map[string]string{"NAME": "Linux"}, fallback: "Docker OS", want: "Linux"},
		{name: "docker fallback", release: map[string]string{}, fallback: "Docker OS", want: "Docker OS"},
		{name: "read error", err: errors.New("missing os-release"), fallback: "Docker OS", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			readOSRelease = func() (map[string]string, error) { return tc.release, tc.err }
			got, err := hostOSName(dockersystem.Info{OperatingSystem: tc.fallback})
			if tc.err != nil {
				if err == nil {
					t.Fatal("expected os-release error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("hostOSName() = %q, want %q", got, tc.want)
			}
		})
	}
}
