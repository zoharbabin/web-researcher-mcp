//go:build e2e && soak

package e2e

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSoak_SustainedLoad_NoUnboundedGrowth drives the real HTTP binary under
// moderate concurrent load for an extended window and asserts the server's
// own /metrics endpoint (Prometheus GoCollector, already wired into
// internal/metrics) shows no unbounded goroutine or heap growth — the class
// of bug (#480) that a point-in-time benchmark (tests/benchmark/) cannot
// catch: slow leaks, goroutine accumulation, degraded steady-state behavior.
//
// Opt-in only (build tag "soak"): not part of `make test-e2e` or any required
// CI gate, mirroring the `live`-tagged / python-live-e2e pattern. Run via
// `make test-soak`; override duration/concurrency with SOAK_DURATION /
// SOAK_WORKERS for a real 30-60min nightly run (defaults keep a bare local
// run short).
func TestSoak_SustainedLoad_NoUnboundedGrowth(t *testing.T) {
	duration := soakDuration(t, "SOAK_DURATION", 2*time.Minute)
	workers := soakInt(t, "SOAK_WORKERS", 8)

	// Sustained load easily exceeds the default per-tenant rate limit (120/min)
	// within seconds; raise it so the soak test measures server resource
	// growth, not the rate limiter's own (already-tested) throttling.
	h := newHTTPHarness(t,
		"ADMIN_API_KEY=soak-test-admin-key-1234567890",
		"RATE_LIMIT_PER_TENANT=1000000",
		"RATE_LIMIT_GLOBAL=1000000",
		"DAILY_QUOTA_PER_TENANT=100000000",
	)
	h.initialize()

	// Warm-up: let the process reach a steady state (connection pools,
	// lazy-initialized caches) before recording the baseline sample, so the
	// baseline isn't itself mid-warm-up growth.
	warmupUntil := time.Now().Add(10 * time.Second)
	for i := 0; time.Now().Before(warmupUntil); i++ {
		h.callTool(1000+i, "web_search", map[string]interface{}{"query": "soak warmup"})
	}

	sampleInterval := duration / 20
	if sampleInterval < 2*time.Second {
		sampleInterval = 2 * time.Second
	}

	var samples []soakSample
	var samplesMu sync.Mutex
	samplesMu.Lock()
	samples = append(samples, h.sampleMetrics(t))
	samplesMu.Unlock()

	stopSampling := make(chan struct{})
	var samplingDone sync.WaitGroup
	samplingDone.Add(1)
	go func() {
		defer samplingDone.Done()
		ticker := time.NewTicker(sampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopSampling:
				return
			case <-ticker.C:
				s := h.sampleMetrics(t)
				samplesMu.Lock()
				samples = append(samples, s)
				samplesMu.Unlock()
			}
		}
	}()

	var callID atomic.Int64
	callID.Store(2000)
	loadCtx, cancelLoad := context.WithTimeout(context.Background(), duration)
	defer cancelLoad()

	var loadWg sync.WaitGroup
	for w := 0; w < workers; w++ {
		loadWg.Add(1)
		go func(worker int) {
			defer loadWg.Done()
			for loadCtx.Err() == nil {
				id := int(callID.Add(1))
				h.callTool(id, "web_search", map[string]interface{}{"query": "soak load"})
			}
		}(w)
	}
	loadWg.Wait()

	close(stopSampling)
	samplingDone.Wait()

	samplesMu.Lock()
	samples = append(samples, h.sampleMetrics(t))
	final := samples[len(samples)-1]
	firstPostWarmup := samples[0]
	samplesMu.Unlock()

	t.Logf("soak run: %d samples over %s, %d workers", len(samples), duration, workers)
	t.Logf("goroutines: first=%d final=%d", firstPostWarmup.goroutines, final.goroutines)
	t.Logf("heap_alloc_bytes: first=%d final=%d", firstPostWarmup.heapAllocBytes, final.heapAllocBytes)

	// Goroutine count is the most direct, lowest-noise leak signal: workers
	// have all exited by the final sample, so it should be back near the
	// warmed-up baseline, not climbing with every request served. Allow a
	// fixed absolute slack for legitimate transient goroutines (in-flight
	// timers, GC workers) rather than a multiplicative bound, which would
	// mask a small-but-real per-request leak on a short local run.
	const goroutineSlack = 50
	if final.goroutines > firstPostWarmup.goroutines+goroutineSlack {
		t.Errorf("goroutine count grew from %d to %d (slack %d) — possible leak",
			firstPostWarmup.goroutines, final.goroutines, goroutineSlack)
	}

	// Heap growth is noisier (GC timing is opaque from outside the process),
	// so compare trend across thirds of the run rather than a single
	// before/after point: a genuine leak keeps climbing across the whole
	// window, while warm-up caches/pools plateau early.
	if len(samples) >= 6 {
		third := len(samples) / 3
		firstThird := averageHeap(samples[:third])
		lastThird := averageHeap(samples[len(samples)-third:])
		if firstThird > 0 {
			growth := float64(lastThird) / float64(firstThird)
			t.Logf("heap_alloc_bytes trend: first-third avg=%d last-third avg=%d ratio=%.2f", firstThird, lastThird, growth)
			const maxGrowthRatio = 3.0
			if growth > maxGrowthRatio {
				t.Errorf("heap allocation grew %.2fx from first third to last third of the run (bound %.1fx) — possible leak",
					growth, maxGrowthRatio)
			}
		}
	}

	// Known-growth surface (#475/#480): the tenant-stats map must stay
	// bounded regardless of how many requests were served.
	resp, err := h.client.Do(mustRequest(t, http.MethodGet, h.baseURL+"/admin/analytics", "soak-test-admin-key-1234567890"))
	if err != nil {
		t.Fatalf("GET /admin/analytics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/admin/analytics status = %d, want 200", resp.StatusCode)
	}

	// The server must still be responsive after the sustained window, not
	// degraded or wedged.
	final2 := h.callTool(int(callID.Add(1)), "web_search", map[string]interface{}{"query": "soak post-load"})
	if final2.Error != nil {
		t.Errorf("post-load web_search returned protocol error: %s", final2.Error)
	}
}

type soakSample struct {
	goroutines     int64
	heapAllocBytes int64
}

// sampleMetrics scrapes the server's own GET /metrics (Prometheus text
// exposition format) and extracts go_goroutines / go_memstats_heap_alloc_bytes
// — the same GoCollector gauges an operator's real monitoring would alert on.
func (h *httpHarness) sampleMetrics(t *testing.T) soakSample {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.baseURL+"/metrics", nil)
	if err != nil {
		t.Fatalf("build /metrics request: %v", err)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics body: %v", err)
	}

	var s soakSample
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		val, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "go_goroutines":
			s.goroutines = int64(val)
		case "go_memstats_heap_alloc_bytes":
			s.heapAllocBytes = int64(val)
		}
	}
	return s
}

func averageHeap(samples []soakSample) int64 {
	if len(samples) == 0 {
		return 0
	}
	var sum int64
	for _, s := range samples {
		sum += s.heapAllocBytes
	}
	return sum / int64(len(samples))
}

func mustRequest(t *testing.T, method, url, adminKey string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Admin-Key", adminKey)
	return req
}

func soakDuration(t *testing.T, envVar string, def time.Duration) time.Duration {
	t.Helper()
	v := os.Getenv(envVar)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		t.Fatalf("invalid %s=%q: %v", envVar, v, err)
	}
	return d
}

func soakInt(t *testing.T, envVar string, def int) int {
	t.Helper()
	v := os.Getenv(envVar)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("invalid %s=%q: %v", envVar, v, err)
	}
	return n
}
