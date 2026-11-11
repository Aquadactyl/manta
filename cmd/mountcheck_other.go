//go:build !darwin

package cmd

import "context"

// startMountCheck does nothing, the check only exists for macOS.
func startMountCheck(_ context.Context) {}
