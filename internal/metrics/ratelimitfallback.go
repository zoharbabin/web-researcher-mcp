package metrics

import "github.com/prometheus/client_golang/prometheus"

// RedisFallbackSource reports the cumulative count of rate-limiter daily-quota
// checks that fell back to the per-pod local counter because the Redis
// incrementer errored. ratelimit.Limiter satisfies this via its exported
// atomic; metrics depends on this small interface (not on internal/ratelimit)
// to avoid an import cycle.
type RedisFallbackSource interface {
	FallbackCount() int64
}

// RegisterRateLimitFallback exposes the rate limiter's Redis-fallback count as
// a Prometheus counter so a flaky Redis silently degrading cross-pod quota
// enforcement to per-pod is observable/alertable (#470) instead of buried in
// an unread atomic. Safe to call once at startup; a nil source is ignored.
func (c *Collector) RegisterRateLimitFallback(src RedisFallbackSource) {
	if src == nil {
		return
	}
	c.registry.MustRegister(rateLimitFallbackCollector{src: src})
}

type rateLimitFallbackCollector struct{ src RedisFallbackSource }

var rateLimitFallbackDesc = prometheus.NewDesc("mcp_ratelimit_redis_fallback_total",
	"Daily-quota checks that fell back to the per-pod local counter after a Redis incrementer error", nil, nil)

func (r rateLimitFallbackCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- rateLimitFallbackDesc
}

func (r rateLimitFallbackCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(rateLimitFallbackDesc, prometheus.CounterValue, float64(r.src.FallbackCount()))
}
