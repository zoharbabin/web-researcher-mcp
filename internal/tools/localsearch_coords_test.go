package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// capturingLocalProvider records the LocalSearchParams it receives so a test can
// assert the tool layer forwards latitude/longitude/radius (F2) verbatim.
type capturingLocalProvider struct {
	last search.LocalSearchParams
}

func (m *capturingLocalProvider) Name() string { return "brave" }
func (m *capturingLocalProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "paid", Description: "capturing local"}
}
func (m *capturingLocalProvider) Local(_ context.Context, params search.LocalSearchParams) ([]search.LocalResult, error) {
	m.last = params
	return []search.LocalResult{{ID: "x", Name: "Place", Source: "brave"}}, nil
}

// localDepsWithCapture wires a capturing provider as the sole local provider.
func localDepsWithCapture() (Dependencies, *capturingLocalProvider) {
	deps := setupTestDeps()
	cap := &capturingLocalProvider{}
	deps.LocalProviders = map[string]search.LocalProvider{cap.Name(): cap}
	return deps, cap
}

// emptyLocalProvider returns no places, to exercise local_search's zero-result
// hints branch (the localFilterMap(input, lat, lon) call, issue #357).
type emptyLocalProvider struct{}

func (m *emptyLocalProvider) Name() string { return "brave" }
func (m *emptyLocalProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "paid", Description: "empty local"}
}
func (m *emptyLocalProvider) Local(_ context.Context, _ search.LocalSearchParams) ([]search.LocalResult, error) {
	return []search.LocalResult{}, nil
}

// localDepsWithEmpty wires a zero-result provider as the sole local provider.
func localDepsWithEmpty() Dependencies {
	deps := setupTestDeps()
	deps.LocalProviders = map[string]search.LocalProvider{"brave": &emptyLocalProvider{}}
	return deps
}

func callLocal(ctx context.Context, t *testing.T, deps Dependencies, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "local_search", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res
}

// TestLocalSearch_CoordinatesForwarded asserts F2's tool-layer surface: a
// latitude/longitude/radius anchor flows through to LocalSearchParams as
// pointers (so an unset anchor is distinguishable from a literal 0,0).
func TestLocalSearch_CoordinatesForwarded(t *testing.T) {
	ctx := context.Background()
	deps, cap := localDepsWithCapture()

	res := callLocal(ctx, t, deps, map[string]any{
		"query":     "coffee",
		"latitude":  47.6062,
		"longitude": -122.3321,
		"radius":    5000.0,
	})
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	if cap.last.Latitude == nil || cap.last.Longitude == nil {
		t.Fatalf("coordinates not forwarded: lat=%v lon=%v", cap.last.Latitude, cap.last.Longitude)
	}
	if *cap.last.Latitude != 47.6062 || *cap.last.Longitude != -122.3321 {
		t.Errorf("coords = %v,%v want 47.6062,-122.3321", *cap.last.Latitude, *cap.last.Longitude)
	}
	if cap.last.Radius != 5000.0 {
		t.Errorf("radius = %v want 5000", cap.last.Radius)
	}
}

// TestLocalSearch_NoCoordinatesAreNil asserts the anchor pointers stay nil when
// the caller omits coordinates — the provider must see "unset", not 0,0.
func TestLocalSearch_NoCoordinatesAreNil(t *testing.T) {
	ctx := context.Background()
	deps, cap := localDepsWithCapture()

	res := callLocal(ctx, t, deps, map[string]any{"query": "coffee", "near": "Seattle"})
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	if cap.last.Latitude != nil || cap.last.Longitude != nil {
		t.Errorf("anchor must be nil when omitted: lat=%v lon=%v", cap.last.Latitude, cap.last.Longitude)
	}
	if cap.last.Near != "Seattle" {
		t.Errorf("near = %q want Seattle", cap.last.Near)
	}
}

// TestLocalSearch_LoneCoordinateRejected asserts a half-specified anchor is a
// boundary validation error, not a silent 0-on-one-axis anchor.
func TestLocalSearch_LoneCoordinateRejected(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"lat only", map[string]any{"query": "coffee", "latitude": 47.6}},
		{"lon only", map[string]any{"query": "coffee", "longitude": -122.3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _ := localDepsWithCapture()
			res := callLocal(ctx, t, deps, tc.args)
			if !res.IsError {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

// TestLocalSearch_OutOfRangeCoordinatesRejected asserts lat/lon bounds are
// enforced at the boundary.
func TestLocalSearch_OutOfRangeCoordinatesRejected(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		lat, lon float64
	}{
		{"lat too high", 91, 0},
		{"lat too low", -91, 0},
		{"lon too high", 0, 181},
		{"lon too low", 0, -181},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _ := localDepsWithCapture()
			res := callLocal(ctx, t, deps, map[string]any{"query": "x", "latitude": tc.lat, "longitude": tc.lon})
			if !res.IsError {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

// TestLocalSearch_NegativeRadiusRejected asserts a negative radius is rejected.
func TestLocalSearch_NegativeRadiusRejected(t *testing.T) {
	ctx := context.Background()
	deps, _ := localDepsWithCapture()
	res := callLocal(ctx, t, deps, map[string]any{
		"query": "x", "latitude": 1.0, "longitude": 2.0, "radius": -1.0,
	})
	if !res.IsError {
		t.Fatal("expected error for negative radius")
	}
}

// TestLocalSearch_ResultShapeUnchanged is a light guard that the success path
// still returns the documented place shape after the coordinate wiring.
func TestLocalSearch_ResultShapeUnchanged(t *testing.T) {
	ctx := context.Background()
	deps, _ := localDepsWithCapture()
	res := callLocal(ctx, t, deps, map[string]any{"query": "coffee"})
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &body); err != nil {
		t.Fatalf("parse content: %v", err)
	}
	if _, ok := body["places"]; !ok {
		t.Error("missing places")
	}
}

// TestLocalSearch_ZeroResultHints asserts local_search attaches a
// ZeroResultHints object whose filtersApplied is built from the caller's
// actual filters (near/country/latitude/longitude/radius) via
// localFilterMap, not a bare nil,nil (issue #357).
func TestLocalSearch_ZeroResultHints(t *testing.T) {
	t.Run("emits hints with lat/lon/radius on zero results", func(t *testing.T) {
		deps := localDepsWithEmpty()
		res := callLocal(context.Background(), t, deps, map[string]any{
			"query":     "coffee",
			"latitude":  47.6062,
			"longitude": -122.3321,
			"radius":    5000.0,
			"country":   "US",
		})
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &body); err != nil {
			t.Fatalf("parse content: %v", err)
		}
		if body["resultCount"].(float64) != 0 {
			t.Fatalf("expected 0 results, got %v", body["resultCount"])
		}
		hints, ok := body["hints"].(map[string]any)
		if !ok {
			t.Fatalf("expected hints object on zero results, got %v", body["hints"])
		}
		filters, ok := hints["filtersApplied"].(map[string]any)
		if !ok {
			t.Fatalf("expected filtersApplied map, got %v", hints["filtersApplied"])
		}
		for _, key := range []string{"latitude", "longitude", "radius", "country"} {
			if _, present := filters[key]; !present {
				t.Errorf("filtersApplied missing %q, got %v", key, filters)
			}
		}
	})

	t.Run("no hints when results present", func(t *testing.T) {
		deps, _ := localDepsWithCapture()
		res := callLocal(context.Background(), t, deps, map[string]any{"query": "coffee"})
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &body); err != nil {
			t.Fatalf("parse content: %v", err)
		}
		if _, present := body["hints"]; present {
			t.Errorf("hints must be absent on non-empty results, got %v", body["hints"])
		}
	})
}

// TestLocalFilterMap asserts localFilterMap collects only the filters the
// caller actually set (issue #357: zero-result hints must reflect the real
// culprit, not a bare unactionable reason from nil,nil).
func TestLocalFilterMap(t *testing.T) {
	t.Run("empty input yields empty map", func(t *testing.T) {
		got := localFilterMap(localSearchInput{}, nil, nil)
		if len(got) != 0 {
			t.Errorf("expected empty map, got %v", got)
		}
	})

	t.Run("collects all set filters", func(t *testing.T) {
		lat, lon := 47.6062, -122.3321
		input := localSearchInput{Near: "Seattle", Country: "US", Radius: 5000}
		got := localFilterMap(input, &lat, &lon)
		want := map[string]string{
			"near":      "Seattle",
			"country":   "US",
			"latitude":  "47.6062",
			"longitude": "-122.3321",
			"radius":    "5000",
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("filter[%q] = %q want %q", k, got[k], v)
			}
		}
		if len(got) != len(want) {
			t.Errorf("got %v want keys %v", got, want)
		}
	})

	t.Run("lone coordinate is not included", func(t *testing.T) {
		got := localFilterMap(localSearchInput{}, nil, nil)
		if _, present := got["latitude"]; present {
			t.Errorf("latitude must not appear without both lat and lon, got %v", got)
		}
	})

	t.Run("zero radius is not included", func(t *testing.T) {
		got := localFilterMap(localSearchInput{Radius: 0}, nil, nil)
		if _, present := got["radius"]; present {
			t.Errorf("radius=0 must not be treated as a set filter, got %v", got)
		}
	})
}

// TestCoordCacheKey asserts the cache-key fragment distinguishes an unset anchor
// from a literal 0,0 and renders deterministically (pointers are dereferenced).
func TestCoordCacheKey(t *testing.T) {
	if got := coordCacheKey(nil, nil); got != "none" {
		t.Errorf("nil anchor = %q want none", got)
	}
	zero := 0.0
	if got := coordCacheKey(&zero, &zero); got == "none" {
		t.Error("0,0 must not collide with the unset anchor")
	}
	lat, lon := 47.6062, -122.3321
	a := coordCacheKey(&lat, &lon)
	lat2, lon2 := 47.6062, -122.3321
	b := coordCacheKey(&lat2, &lon2)
	if a != b {
		t.Errorf("same coords hash differently: %q vs %q", a, b)
	}
}
