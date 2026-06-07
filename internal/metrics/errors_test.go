package metrics

import (
	"strings"
	"testing"
)

func TestErrorRing_NewestFirst(t *testing.T) {
	r := NewErrorRing()
	for _, tool := range []string{"a", "b", "c"} {
		r.Record(ErrorRecord{Tool: tool, Kind: "upstream_error"})
	}
	got := r.Recent("")
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Tool != "c" || got[2].Tool != "a" {
		t.Errorf("order = %v, want newest-first [c b a]", []string{got[0].Tool, got[1].Tool, got[2].Tool})
	}
}

func TestErrorRing_BoundedCapacity(t *testing.T) {
	r := NewErrorRing()
	// Insert well past capacity; oldest must be overwritten.
	for i := 0; i < recentErrorsCap*3; i++ {
		r.Record(ErrorRecord{Tool: "web_search", Kind: "upstream_error"})
	}
	got := r.Recent("")
	if len(got) != recentErrorsCap {
		t.Errorf("len = %d, want cap %d", len(got), recentErrorsCap)
	}
}

func TestErrorRing_RedactsCause(t *testing.T) {
	r := NewErrorRing()
	r.Record(ErrorRecord{
		Tool:  "web_search",
		Kind:  "upstream_error",
		Cause: "google: 400 key=AIzaSyA1234567890123456789012345678901234",
	})
	got := r.Recent("")
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if strings.Contains(got[0].Cause, "AIzaSy") {
		t.Errorf("cause leaked a secret: %q", got[0].Cause)
	}
	if !strings.Contains(got[0].Cause, "[REDACTED]") {
		t.Errorf("cause not redacted: %q", got[0].Cause)
	}
}

func TestErrorRing_TenantScope(t *testing.T) {
	r := NewErrorRing()
	r.Record(ErrorRecord{Tool: "web_search", Kind: "x", TenantID: "t1"})
	r.Record(ErrorRecord{Tool: "news_search", Kind: "y", TenantID: "t2"})
	r.Record(ErrorRecord{Tool: "scrape_page", Kind: "z", TenantID: "t1"})

	t1 := r.Recent("t1")
	if len(t1) != 2 {
		t.Errorf("t1 errors = %d, want 2", len(t1))
	}
	for _, e := range t1 {
		if e.TenantID != "t1" {
			t.Errorf("t1 view leaked tenant %q", e.TenantID)
		}
	}
	all := r.Recent("")
	if len(all) != 3 {
		t.Errorf("global view = %d, want 3", len(all))
	}
}

func TestErrorRing_IgnoresEmptyTool(t *testing.T) {
	r := NewErrorRing()
	r.Record(ErrorRecord{Tool: "", Kind: "x"})
	if got := r.Recent(""); len(got) != 0 {
		t.Errorf("empty-tool record was stored: %v", got)
	}
}

func TestErrorRing_NilSafe(t *testing.T) {
	var r *ErrorRing
	r.Record(ErrorRecord{Tool: "web_search"}) // must not panic
	if got := r.Recent(""); got != nil {
		t.Errorf("nil ring Recent = %v, want nil", got)
	}
}

func TestCollector_RecordAndReadErrors(t *testing.T) {
	c := NewCollector()
	c.RecordError(ErrorRecord{Tool: "web_search", Kind: "rate_limited", Provider: "google", TenantID: "t1"})
	got := c.RecentErrors("t1")
	if len(got) != 1 || got[0].Provider != "google" {
		t.Fatalf("RecentErrors = %+v", got)
	}
	if got[0].Timestamp == "" {
		t.Errorf("timestamp not stamped")
	}
}
