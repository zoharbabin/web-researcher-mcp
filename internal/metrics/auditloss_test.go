package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeLossSource is a deterministic AuditLossSource for the metrics test.
type fakeLossSource struct{ dropped, spilled, rotations int64 }

func (f fakeLossSource) DroppedCount() int64  { return f.dropped }
func (f fakeLossSource) SpilledCount() int64  { return f.spilled }
func (f fakeLossSource) RotationCount() int64 { return f.rotations }

func TestRegisterAuditLossExposesCounters(t *testing.T) {
	c := NewCollector()
	c.RegisterAuditLoss(fakeLossSource{dropped: 3, spilled: 7, rotations: 1})

	// Scrape the /metrics handler and assert the three counters carry the values.
	rec := httptest.NewRecorder()
	c.HTTPHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	for _, want := range []string{
		"mcp_audit_events_dropped_total 3",
		"mcp_audit_events_spilled_total 7",
		"mcp_audit_log_rotations_total 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}

func TestRegisterAuditLossNilIsNoop(t *testing.T) {
	c := NewCollector()
	c.RegisterAuditLoss(nil) // must not panic or register anything
}
