package tools

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

var errUnexpectedToolError = errors.New("tool returned IsError")

// TestSingleflightCoalescesCacheMiss proves #474: N identical concurrent
// econ_search calls that all miss the cache during the same window must
// collapse into exactly 1 upstream Econ() call, while every caller still gets
// a successful, correctly-populated result.
func TestSingleflightCoalescesCacheMiss(t *testing.T) {
	var calls atomic.Int64
	econ := &mockEconProvider{calls: &calls, delay: 50 * time.Millisecond}
	deps := setupTestDeps()
	deps.EconProviders = map[string]search.EconProvider{econ.Name(): econ}

	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "econ_search", Arguments: map[string]any{"query": "gdp"}})
			if err != nil {
				errs <- err
				return
			}
			if res.IsError {
				errs <- errUnexpectedToolError
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent econ_search call failed: %v", err)
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("want exactly 1 upstream Econ() call for %d concurrent identical requests, got %d", n, got)
	}
}

// TestSingleflightDoesNotCoalesceAcrossTenants proves the Isolation fix in
// coalesceKey (#474/#484): two different tenants issuing the identical query
// concurrently must NOT share an in-flight singleflight call — each tenant
// gets its own upstream call. A naive un-scoped dedup key would collapse
// these into 1 call and leak one tenant's result into another's response.
func TestSingleflightDoesNotCoalesceAcrossTenants(t *testing.T) {
	var calls atomic.Int64
	econ := &mockEconProvider{calls: &calls, delay: 50 * time.Millisecond}
	deps := setupTestDeps()
	deps.EconProviders = map[string]search.EconProvider{econ.Name(): econ}

	srvA := createIdentityTestServer(deps, "tenant-a", "user-a")
	srvB := createIdentityTestServer(deps, "tenant-b", "user-b")
	ctx := context.Background()
	sessA := connectTestClient(ctx, t, srvA)
	defer sessA.Close()
	sessB := connectTestClient(ctx, t, srvB)
	defer sessB.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, sess := range []*mcp.ClientSession{sessA, sessB} {
		wg.Add(1)
		go func(sess *mcp.ClientSession) {
			defer wg.Done()
			res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "econ_search", Arguments: map[string]any{"query": "gdp"}})
			if err != nil {
				errs <- err
				return
			}
			if res.IsError {
				errs <- errUnexpectedToolError
			}
		}(sess)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent econ_search call failed: %v", err)
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("want 2 upstream Econ() calls (one per tenant), got %d — cross-tenant coalescing would leak one tenant's result into another's", got)
	}
}
