//go:build !linux

package system

import (
	"context"
	"time"
)

const HostUsageSupported = false

func ReadResourceUsage(_ context.Context, _ string) ResourceUsage {
	return ResourceUsage{SampledAt: time.Now().UTC()}
}
