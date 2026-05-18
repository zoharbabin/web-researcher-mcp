package metrics

import (
	"math"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Collector struct {
	toolCalls   *prometheus.CounterVec
	toolErrors  *prometheus.CounterVec
	toolLatency *prometheus.HistogramVec
	cacheHits   *prometheus.CounterVec
	cacheMisses *prometheus.CounterVec
	activeConns prometheus.Gauge

	mu        sync.RWMutex
	toolStats map[string]*ToolMetrics
	registry  *prometheus.Registry
}

type ToolMetrics struct {
	TotalCalls   atomic.Int64
	SuccessCalls atomic.Int64
	ErrorCalls   atomic.Int64
	CacheHits    atomic.Int64
	latencies    []float64
	latencyMu    sync.Mutex
	LastCalled   time.Time
}

type ToolStatsSnapshot struct {
	TotalCalls   int64   `json:"totalCalls"`
	SuccessCalls int64   `json:"successCalls"`
	ErrorCalls   int64   `json:"errorCalls"`
	CacheHits    int64   `json:"cacheHits"`
	AvgLatencyMs float64 `json:"avgLatencyMs"`
	P95LatencyMs float64 `json:"p95LatencyMs"`
	LastCalled   string  `json:"lastCalled,omitempty"`
}

func NewCollector() *Collector {
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())

	c := &Collector{
		toolCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcp_tool_calls_total",
			Help: "Total MCP tool calls",
		}, []string{"tool"}),
		toolErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcp_tool_errors_total",
			Help: "Total MCP tool errors",
		}, []string{"tool", "error_code"}),
		toolLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "mcp_tool_latency_seconds",
			Help:    "Tool execution latency",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, []string{"tool"}),
		cacheHits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcp_cache_hits_total",
			Help: "Cache hits",
		}, []string{"layer"}),
		cacheMisses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcp_cache_misses_total",
			Help: "Cache misses",
		}, []string{"layer"}),
		activeConns: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mcp_active_connections",
			Help: "Active MCP connections",
		}),
		toolStats: make(map[string]*ToolMetrics),
		registry:  registry,
	}

	registry.MustRegister(c.toolCalls, c.toolErrors, c.toolLatency, c.cacheHits, c.cacheMisses, c.activeConns)
	return c
}

func (c *Collector) RecordToolCall(tool string, latency time.Duration, err error, errCode string, cacheHit bool) {
	c.toolCalls.WithLabelValues(tool).Inc()
	c.toolLatency.WithLabelValues(tool).Observe(latency.Seconds())

	if cacheHit {
		c.cacheHits.WithLabelValues("tool").Inc()
	}

	stats := c.getOrCreateStats(tool)
	stats.TotalCalls.Add(1)
	stats.LastCalled = time.Now()

	if err != nil {
		stats.ErrorCalls.Add(1)
		c.toolErrors.WithLabelValues(tool, errCode).Inc()
	} else {
		stats.SuccessCalls.Add(1)
	}

	if cacheHit {
		stats.CacheHits.Add(1)
	}

	stats.latencyMu.Lock()
	stats.latencies = append(stats.latencies, float64(latency.Milliseconds()))
	if len(stats.latencies) > 1000 {
		stats.latencies = stats.latencies[len(stats.latencies)-1000:]
	}
	stats.latencyMu.Unlock()
}

func (c *Collector) RecordCacheHit(layer string) {
	c.cacheHits.WithLabelValues(layer).Inc()
}

func (c *Collector) RecordCacheMiss(layer string) {
	c.cacheMisses.WithLabelValues(layer).Inc()
}

func (c *Collector) IncrConnections() {
	c.activeConns.Inc()
}

func (c *Collector) DecrConnections() {
	c.activeConns.Dec()
}

func (c *Collector) GetToolStats() map[string]ToolStatsSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]ToolStatsSnapshot, len(c.toolStats))
	for name, stats := range c.toolStats {
		snap := ToolStatsSnapshot{
			TotalCalls:   stats.TotalCalls.Load(),
			SuccessCalls: stats.SuccessCalls.Load(),
			ErrorCalls:   stats.ErrorCalls.Load(),
			CacheHits:    stats.CacheHits.Load(),
		}
		if !stats.LastCalled.IsZero() {
			snap.LastCalled = stats.LastCalled.Format(time.RFC3339)
		}

		stats.latencyMu.Lock()
		if len(stats.latencies) > 0 {
			snap.AvgLatencyMs = avg(stats.latencies)
			snap.P95LatencyMs = percentile(stats.latencies, 95)
		}
		stats.latencyMu.Unlock()

		result[name] = snap
	}
	return result
}

func (c *Collector) HTTPHandler() http.Handler {
	return promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{})
}

func (c *Collector) getOrCreateStats(tool string) *ToolMetrics {
	c.mu.RLock()
	if stats, ok := c.toolStats[tool]; ok {
		c.mu.RUnlock()
		return stats
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if stats, ok := c.toolStats[tool]; ok {
		return stats
	}
	stats := &ToolMetrics{}
	c.toolStats[tool] = stats
	return stats
}

func avg(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return math.Round(sum/float64(len(vals))*100) / 100
}

func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
