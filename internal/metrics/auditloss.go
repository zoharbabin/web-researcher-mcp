package metrics

import "github.com/prometheus/client_golang/prometheus"

// AuditLossSource reports cumulative audit-pipeline loss counters. The audit
// Logger satisfies this via its exported atomics; metrics depends on this small
// interface (not on the audit package) to avoid an import cycle.
type AuditLossSource interface {
	DroppedCount() int64  // events dropped entirely (buffer + swap full)
	SpilledCount() int64  // events spilled from the in-memory buffer to swap
	RotationCount() int64 // audit-file rotations
}

// RegisterAuditLoss exposes audit-pipeline loss as Prometheus counters so audit
// loss under sustained backpressure is observable/alertable instead of being
// buried in unread atomics (OWASP Agentic ASI07 traceability). Safe to call once
// at startup; a nil source is ignored.
func (c *Collector) RegisterAuditLoss(src AuditLossSource) {
	if src == nil {
		return
	}
	c.registry.MustRegister(auditLossCollector{src: src})
}

type auditLossCollector struct{ src AuditLossSource }

var (
	auditDroppedDesc = prometheus.NewDesc("mcp_audit_events_dropped_total",
		"Audit events dropped (in-memory buffer and swap both full)", nil, nil)
	auditSpilledDesc = prometheus.NewDesc("mcp_audit_events_spilled_total",
		"Audit events spilled from the in-memory buffer to swap under backpressure", nil, nil)
	auditRotationsDesc = prometheus.NewDesc("mcp_audit_log_rotations_total",
		"Audit log file rotations", nil, nil)
)

func (a auditLossCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- auditDroppedDesc
	ch <- auditSpilledDesc
	ch <- auditRotationsDesc
}

func (a auditLossCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(auditDroppedDesc, prometheus.CounterValue, float64(a.src.DroppedCount()))
	ch <- prometheus.MustNewConstMetric(auditSpilledDesc, prometheus.CounterValue, float64(a.src.SpilledCount()))
	ch <- prometheus.MustNewConstMetric(auditRotationsDesc, prometheus.CounterValue, float64(a.src.RotationCount()))
}
