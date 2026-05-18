package metrics

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRecordToolCall(t *testing.T) {
	c := NewCollector()

	c.RecordToolCall("web_search", 100*time.Millisecond, nil, "", false)
	c.RecordToolCall("web_search", 200*time.Millisecond, nil, "", false)
	c.RecordToolCall("web_search", 50*time.Millisecond, errors.New("fail"), "timeout", false)

	stats := c.GetToolStats()
	s := stats["web_search"]

	if s.TotalCalls != 3 {
		t.Fatalf("expected 3 total calls, got %d", s.TotalCalls)
	}
	if s.SuccessCalls != 2 {
		t.Fatalf("expected 2 success calls, got %d", s.SuccessCalls)
	}
	if s.ErrorCalls != 1 {
		t.Fatalf("expected 1 error call, got %d", s.ErrorCalls)
	}
}

func TestRecordCacheHit(t *testing.T) {
	c := NewCollector()

	c.RecordToolCall("scrape_page", 10*time.Millisecond, nil, "", true)

	stats := c.GetToolStats()
	s := stats["scrape_page"]

	if s.CacheHits != 1 {
		t.Fatalf("expected 1 cache hit, got %d", s.CacheHits)
	}
}

func TestLatencyStats(t *testing.T) {
	c := NewCollector()

	for i := 0; i < 100; i++ {
		c.RecordToolCall("test_tool", time.Duration(i+1)*time.Millisecond, nil, "", false)
	}

	stats := c.GetToolStats()
	s := stats["test_tool"]

	if s.AvgLatencyMs == 0 {
		t.Fatal("expected non-zero average latency")
	}
	if s.P95LatencyMs == 0 {
		t.Fatal("expected non-zero P95 latency")
	}
	if s.P95LatencyMs <= s.AvgLatencyMs {
		t.Fatalf("P95 (%f) should be greater than avg (%f)", s.P95LatencyMs, s.AvgLatencyMs)
	}
}

func TestReservoirSampling(t *testing.T) {
	c := NewCollector()

	for i := 0; i < 1500; i++ {
		c.RecordToolCall("heavy_tool", time.Millisecond, nil, "", false)
	}

	stats := c.getOrCreateStats("heavy_tool")
	stats.latencyMu.Lock()
	count := len(stats.latencies)
	stats.latencyMu.Unlock()

	if count > 1000 {
		t.Fatalf("expected at most 1000 latency samples, got %d", count)
	}
}

func TestConnections(t *testing.T) {
	c := NewCollector()
	c.IncrConnections()
	c.IncrConnections()
	c.DecrConnections()
	// Should not panic
}

func TestHTTPHandler(t *testing.T) {
	c := NewCollector()
	c.RecordToolCall("web_search", time.Millisecond, nil, "", false)

	handler := c.HTTPHandler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected non-empty metrics response")
	}
}

func TestMultipleTools(t *testing.T) {
	c := NewCollector()

	c.RecordToolCall("web_search", time.Millisecond, nil, "", false)
	c.RecordToolCall("scrape_page", time.Millisecond, nil, "", false)
	c.RecordToolCall("news_search", time.Millisecond, nil, "", false)

	stats := c.GetToolStats()
	if len(stats) != 3 {
		t.Fatalf("expected 3 tools in stats, got %d", len(stats))
	}
}

func TestAvg(t *testing.T) {
	tests := []struct {
		vals []float64
		want float64
	}{
		{nil, 0},
		{[]float64{10}, 10},
		{[]float64{10, 20, 30}, 20},
	}

	for _, tt := range tests {
		got := avg(tt.vals)
		if got != tt.want {
			t.Errorf("avg(%v) = %f, want %f", tt.vals, got, tt.want)
		}
	}
}

func TestPercentile(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	p95 := percentile(vals, 95)
	if p95 < 9 {
		t.Fatalf("expected P95 >= 9, got %f", p95)
	}

	p50 := percentile(vals, 50)
	if p50 < 4 || p50 > 6 {
		t.Fatalf("expected P50 around 5, got %f", p50)
	}

	empty := percentile(nil, 95)
	if empty != 0 {
		t.Fatalf("expected 0 for empty slice, got %f", empty)
	}
}
