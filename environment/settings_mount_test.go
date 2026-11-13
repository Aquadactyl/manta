package environment

import (
	"testing"
)

func TestSELinuxBinds(t *testing.T) {
	mounts := []Mount{
		{Source: "/host/data", Target: "/container/data"},
		{Source: "/host/config", Target: "/container/config", ReadOnly: true},
	}
	tests := []struct {
		mode    string
		want    []string
		wantErr bool
	}{
		{mode: "shared", want: []string{"/host/data:/container/data:z", "/host/config:/container/config:ro,z"}},
		{mode: "private", want: []string{"/host/data:/container/data:Z", "/host/config:/container/config:ro,Z"}},
		{mode: "invalid", wantErr: true},
	}
	for _, tt := range tests {
		got, err := SELinuxBinds(mounts, tt.mode)
		if (err != nil) != tt.wantErr {
			t.Fatalf("mode %q error = %v", tt.mode, err)
		}
		if tt.wantErr {
			continue
		}
		if len(got) != len(tt.want) {
			t.Fatalf("mode %q got %#v, want %#v", tt.mode, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("mode %q bind %d = %q, want %q", tt.mode, i, got[i], tt.want[i])
			}
		}
	}
}
