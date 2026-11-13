//go:build !darwin

package cmd

import (
	"context"
	"testing"
)

func TestStartMountCheckIsNoopOffDarwin(t *testing.T) {
	startMountCheck(context.Background())
}
