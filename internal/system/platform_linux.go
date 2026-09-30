//go:build linux

package system

import (
	"context"

	"github.com/aduthekaddu/relay/internal/api"
)

// platformMetrics samples metrics from /proc and /sys.
func platformMetrics(ctx context.Context, c *Collector) api.Metrics { return c.Sample(ctx) }
