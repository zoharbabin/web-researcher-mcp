package search

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// =============================================================================
// Brave provider — issue #261 contract tests (F1–F10).
//
// These complement the Brave cases already in search_test.go. Each test stands
// up an httptest server, redirects api.search.brave.com to it via
// rewriteTransport, captures the outbound request (query + headers), and
// asserts the wire contract Brave actually documents. Pure httptest, no
// network, safe under -race.
// =============================================================================

// newBraveTestProvider wires a BraveProvider to a captured test server. The
// handler records the last request's query + headers into the returned pointers
// and replies with body (or "{}" when empty).
func newBraveTestProvider(t *testing.T, cfg BraveConfig, handler http.HandlerFunc) (*BraveProvider, func()) {
	t.Helper()
	ts := httptest.NewServer(handler)
	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60})}
	return NewBraveProvider("brave-key", cfg, deps), ts.Close
}

// --- A. Web request contract --------------------------------------------------

// TestBraveWeb_PinsApiVersion asserts every Brave web call carries the pinned
// Api-Version header (F10) so a future Brave schema change can't silently break
// our parsers.
func TestBraveWeb_PinsApiVersion(t *testing.T) {
	t.Parallel()
	var gotVersion string
	b, closeFn := newBraveTestProvider(t, BraveConfig{}, func(w http.ResponseWriter, r *http.Request) {
		gotVersion = r.Header.Get("Api-Version")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	})
	defer closeFn()

	if _, err := b.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotVersion != braveAPIVersion {
		t.Errorf("Api-Version = %q, want %q", gotVersion, braveAPIVersion)
	}
}

// TestBraveAPIVersion_ScopedToWebProduct guards the F10 scoping fix: the
// Api-Version pin is valid ONLY on the web-search product. Brave rejects an
// explicit Api-Version on every other product — images/news with a 404
// API_VERSION_NOT_FOUND, local (pois/descriptions) and llm/context with 422 —
// so the header MUST ride on /web/search calls (including the local pipeline's
// step-1 web/search?result_filter=locations) and MUST NOT ride on any other
// endpoint. A captured live regression (e2e brave/image_search + news_search
// 404s) is pinned here as a unit contract so it can't recur.
func TestBraveAPIVersion_ScopedToWebProduct(t *testing.T) {
	t.Parallel()

	// versionByPath records the Api-Version header seen per endpoint path so a
	// single multi-leg pipeline (local) can be asserted leg-by-leg.
	type capture struct {
		versionByPath map[string]string
	}
	newServer := func(cap *capture) *BraveProvider {
		cap.versionByPath = map[string]string{}
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cap.versionByPath[r.URL.Path] = r.Header.Get("Api-Version")
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasPrefix(r.URL.Path, "/res/v1/web/search"):
				// Serve a location id so the local pipeline proceeds to pois/desc.
				fmt.Fprint(w, `{"web":{"results":[]},"locations":{"results":[{"id":"loc1"}]}}`)
			case strings.HasPrefix(r.URL.Path, "/res/v1/local/pois"):
				fmt.Fprint(w, `{"results":[{"id":"loc1","title":"Cafe","coordinates":[1,2]}]}`)
			default:
				fmt.Fprint(w, `{"results":[]}`)
			}
		}))
		t.Cleanup(ts.Close)
		client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
		deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60})}
		return NewBraveProvider("brave-key", BraveConfig{}, deps)
	}

	const webPath = "/res/v1/web/search"

	// Web search: version present.
	t.Run("web sends version", func(t *testing.T) {
		t.Parallel()
		var cap capture
		b := newServer(&cap)
		if _, err := b.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 5}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cap.versionByPath[webPath] != braveAPIVersion {
			t.Errorf("web Api-Version = %q, want %q", cap.versionByPath[webPath], braveAPIVersion)
		}
	})

	// Images: version absent (Brave 404s on an explicit version).
	t.Run("images omit version", func(t *testing.T) {
		t.Parallel()
		var cap capture
		b := newServer(&cap)
		if _, err := b.Images(context.Background(), ImageSearchParams{Query: "q", NumResults: 5}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v := cap.versionByPath["/res/v1/images/search"]; v != "" {
			t.Errorf("images must NOT send Api-Version, got %q", v)
		}
	})

	// News: version absent (Brave 404s on an explicit version).
	t.Run("news omit version", func(t *testing.T) {
		t.Parallel()
		var cap capture
		b := newServer(&cap)
		if _, err := b.News(context.Background(), NewsSearchParams{Query: "q", NumResults: 5}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v := cap.versionByPath["/res/v1/news/search"]; v != "" {
			t.Errorf("news must NOT send Api-Version, got %q", v)
		}
	})

	// Context (llm/context): version absent (Brave 422s on an explicit version).
	t.Run("context omits version", func(t *testing.T) {
		t.Parallel()
		var cap capture
		b := newServer(&cap)
		if _, err := b.Context(context.Background(), ContextParams{Query: "q"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v := cap.versionByPath["/res/v1/llm/context"]; v != "" {
			t.Errorf("llm/context must NOT send Api-Version, got %q", v)
		}
	})

	// Local pipeline: version on step-1 web/search ONLY, absent on pois/descriptions.
	t.Run("local sends version on web leg only", func(t *testing.T) {
		t.Parallel()
		var cap capture
		b := newServer(&cap)
		if _, err := b.Local(context.Background(), LocalSearchParams{Query: "coffee", NumResults: 5}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cap.versionByPath[webPath] != braveAPIVersion {
			t.Errorf("local step-1 web Api-Version = %q, want %q", cap.versionByPath[webPath], braveAPIVersion)
		}
		if v := cap.versionByPath["/res/v1/local/pois"]; v != "" {
			t.Errorf("local/pois must NOT send Api-Version, got %q", v)
		}
		if v, seen := cap.versionByPath["/res/v1/local/descriptions"]; seen && v != "" {
			t.Errorf("local/descriptions must NOT send Api-Version, got %q", v)
		}
	})
}

// TestBraveWeb_SafeSearchThreeLevels asserts F8's three-level safesearch
// mapping: off/moderate/strict are preserved (not collapsed to moderate), and
// an empty level omits the param so Brave applies its own default.
func TestBraveWeb_SafeSearchThreeLevels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		safe string
		want string // "" means the param must be absent
	}{
		{"", ""},
		{"off", "off"},
		{"moderate", "moderate"},
		{"medium", "moderate"},
		{"strict", "strict"},
		{"high", "strict"},
		{"bogus", "moderate"}, // unknown → safe default
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.safe, func(t *testing.T) {
			t.Parallel()
			var got url.Values
			b, closeFn := newBraveTestProvider(t, BraveConfig{}, func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("{}"))
			})
			defer closeFn()

			if _, err := b.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 5, Safe: tt.safe}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Get("safesearch") != tt.want {
				t.Errorf("safe=%q → safesearch=%q, want %q", tt.safe, got.Get("safesearch"), tt.want)
			}
		})
	}
}

// TestBraveWeb_CountryAndLanguage asserts locale params reach the wire under
// Brave's documented names (country, search_lang).
func TestBraveWeb_CountryAndLanguage(t *testing.T) {
	t.Parallel()
	var got url.Values
	b, closeFn := newBraveTestProvider(t, BraveConfig{}, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	})
	defer closeFn()

	if _, err := b.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 5, Country: "de", Language: "de"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Get("country") != "de" {
		t.Errorf("country = %q, want %q", got.Get("country"), "de")
	}
	if got.Get("search_lang") != "de" {
		t.Errorf("search_lang = %q, want %q", got.Get("search_lang"), "de")
	}
}

// TestBraveWeb_MultipleGoggles asserts goggles stack as repeated params (F1),
// are bounded to the first 3 slice positions (index ≥ 3 is dropped), empty
// entries within that window are skipped, and the deprecated goggles_id spelling
// is never sent.
func TestBraveWeb_MultipleGoggles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "stacks repeated params",
			input: []string{"https://g1/a.goggle", "https://g2/b.goggle"},
			want:  []string{"https://g1/a.goggle", "https://g2/b.goggle"},
		},
		{
			name:  "caps at first 3 positions",
			input: []string{"https://g1/a.goggle", "https://g2/b.goggle", "https://g3/c.goggle", "https://g4/d.goggle"},
			want:  []string{"https://g1/a.goggle", "https://g2/b.goggle", "https://g3/c.goggle"},
		},
		{
			name:  "empty within window is skipped, consuming its slot",
			input: []string{"https://g1/a.goggle", "", "https://g2/b.goggle", "https://g3/c.goggle"},
			want:  []string{"https://g1/a.goggle", "https://g2/b.goggle"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got url.Values
			b, closeFn := newBraveTestProvider(t, BraveConfig{}, func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("{}"))
			})
			defer closeFn()

			if _, err := b.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 5, Goggles: tt.input}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotGoggles := got["goggles"]
			if len(gotGoggles) != len(tt.want) {
				t.Fatalf("goggles = %v, want %v", gotGoggles, tt.want)
			}
			for i := range tt.want {
				if gotGoggles[i] != tt.want[i] {
					t.Errorf("goggles[%d] = %q, want %q", i, gotGoggles[i], tt.want[i])
				}
			}
			if got.Get("goggles_id") != "" {
				t.Errorf("deprecated goggles_id must never be sent, got %q", got.Get("goggles_id"))
			}
		})
	}
}

// TestBraveWeb_OffsetWithinRange asserts an in-range offset passes through
// unchanged and offset=0 is omitted (the default page, no param needed).
func TestBraveWeb_OffsetWithinRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		offset int
		want   string // "" → param absent
	}{
		{0, ""},
		{5, "5"},
		{9, "9"},
		{100, "9"}, // clamped
	}
	for _, tt := range tests {
		tt := tt
		t.Run(fmt.Sprintf("offset_%d", tt.offset), func(t *testing.T) {
			t.Parallel()
			var got url.Values
			b, closeFn := newBraveTestProvider(t, BraveConfig{}, func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("{}"))
			})
			defer closeFn()

			if _, err := b.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 5, Offset: tt.offset}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Get("offset") != tt.want {
				t.Errorf("offset=%d → wire offset=%q, want %q", tt.offset, got.Get("offset"), tt.want)
			}
		})
	}
}

// TestBraveWeb_MoreResultsAvailable asserts F8: when the tool layer installs a
// ResultMeta collector, Brave's query.more_results_available flag is surfaced
// through it; and that without a collector the parse is still nil-safe.
func TestBraveWeb_MoreResultsAvailable(t *testing.T) {
	t.Parallel()
	makeProvider := func(more bool) (*BraveProvider, func()) {
		return newBraveTestProvider(t, BraveConfig{}, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"query":{"more_results_available":%t},"web":{"results":[]}}`, more)
		})
	}

	// more=true is surfaced as (true, true).
	bTrue, closeTrue := makeProvider(true)
	defer closeTrue()
	ctx, meta := NewResultMeta(context.Background())
	if _, err := bTrue.Web(ctx, WebSearchParams{Query: "q", NumResults: 5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := meta.MoreResultsAvailable(); !ok || !v {
		t.Errorf("more_results_available = (%v,%v), want (true,true)", v, ok)
	}

	// more=false is surfaced as (false, true) — reported, not unknown.
	bFalse, closeFalse := makeProvider(false)
	defer closeFalse()
	ctx2, meta2 := NewResultMeta(context.Background())
	if _, err := bFalse.Web(ctx2, WebSearchParams{Query: "q", NumResults: 5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := meta2.MoreResultsAvailable(); !ok || v {
		t.Errorf("more_results_available = (%v,%v), want (false,true)", v, ok)
	}

	// No collector installed → provider must not panic (nil-safe side channel).
	bNil, closeNil := makeProvider(true)
	defer closeNil()
	if _, err := bNil.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 5}); err != nil {
		t.Fatalf("unexpected error without ResultMeta: %v", err)
	}
}

// --- B. Local pipeline contract ----------------------------------------------

// localHeaders records the headers seen on each leg of the local pipeline.
type localHeaders struct {
	webLoc      http.Header
	poisLoc     http.Header
	webQuery    url.Values
	poisHasIDs  bool
	descHasIDs  bool
	descPresent bool
}

// braveLocalServer stands up a three-endpoint local pipeline returning two
// POIs with coordinates for distance-rank tests.
func braveLocalServer(t *testing.T, caps *localHeaders) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/res/v1/web/search"):
			caps.webLoc = r.Header.Clone()
			caps.webQuery = r.URL.Query()
			fmt.Fprint(w, `{"locations":{"results":[{"id":"near"},{"id":"far"}]}}`)
		case strings.HasPrefix(r.URL.Path, "/res/v1/local/pois"):
			caps.poisLoc = r.Header.Clone()
			caps.poisHasIDs = len(r.URL.Query()["ids"]) > 0
			// "near" ~1km from anchor (47.6062,-122.3321); "far" ~50km away.
			fmt.Fprint(w, `{"results":[
				{"id":"near","title":"Near Cafe","coordinates":[47.6150,-122.3321]},
				{"id":"far","title":"Far Cafe","coordinates":[48.0500,-122.3321]}
			]}`)
		case strings.HasPrefix(r.URL.Path, "/res/v1/local/descriptions"):
			caps.descPresent = true
			caps.descHasIDs = len(r.URL.Query()["ids"]) > 0
			fmt.Fprint(w, `{"results":[]}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}
}

// TestBraveLocal_LocationHeadersCoordinates asserts F2: coordinates ride the
// X-Loc-Lat/Long headers on step 1 ONLY, the query carries no "near …" suffix,
// and no X-Loc-* leaks onto the /local/pois leg.
func TestBraveLocal_LocationHeadersCoordinates(t *testing.T) {
	t.Parallel()
	var caps localHeaders
	b, closeFn := newBraveTestProvider(t, BraveConfig{}, braveLocalServer(t, &caps))
	defer closeFn()

	lat, lon := 47.6062, -122.3321
	_, err := b.Local(context.Background(), LocalSearchParams{
		Query: "coffee", Near: "Seattle", Latitude: &lat, Longitude: &lon, NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caps.webLoc.Get("X-Loc-Lat") != "47.6062" {
		t.Errorf("X-Loc-Lat = %q, want 47.6062", caps.webLoc.Get("X-Loc-Lat"))
	}
	if caps.webLoc.Get("X-Loc-Long") != "-122.3321" {
		t.Errorf("X-Loc-Long = %q, want -122.3321", caps.webLoc.Get("X-Loc-Long"))
	}
	// Coordinates take precedence over Near → no text city header sent.
	if caps.webLoc.Get("X-Loc-City") != "" {
		t.Errorf("X-Loc-City should be empty when coords present, got %q", caps.webLoc.Get("X-Loc-City"))
	}
	// The query must be the bare term — no "near Seattle" suffix biasing the index.
	if q := caps.webQuery.Get("q"); q != "coffee" {
		t.Errorf("step-1 query = %q, want %q (no near-suffix)", q, "coffee")
	}
	// X-Loc-* must NOT appear on the POI leg (Brave's reference client omits it).
	if caps.poisLoc.Get("X-Loc-Lat") != "" || caps.poisLoc.Get("X-Loc-Long") != "" || caps.poisLoc.Get("X-Loc-City") != "" {
		t.Errorf("X-Loc-* leaked onto /local/pois: %v", caps.poisLoc)
	}
}

// TestBraveLocal_LocationHeadersTextFallback asserts that with no coordinates,
// the free-text Near anchors via X-Loc-City and Country via X-Loc-Country.
func TestBraveLocal_LocationHeadersTextFallback(t *testing.T) {
	t.Parallel()
	var caps localHeaders
	b, closeFn := newBraveTestProvider(t, BraveConfig{}, braveLocalServer(t, &caps))
	defer closeFn()

	_, err := b.Local(context.Background(), LocalSearchParams{
		Query: "coffee", Near: "Brooklyn", Country: "us", NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caps.webLoc.Get("X-Loc-City") != "Brooklyn" {
		t.Errorf("X-Loc-City = %q, want Brooklyn", caps.webLoc.Get("X-Loc-City"))
	}
	if caps.webLoc.Get("X-Loc-Country") != "us" {
		t.Errorf("X-Loc-Country = %q, want us", caps.webLoc.Get("X-Loc-Country"))
	}
	if caps.webLoc.Get("X-Loc-Lat") != "" {
		t.Errorf("X-Loc-Lat must be empty in text fallback, got %q", caps.webLoc.Get("X-Loc-Lat"))
	}
}

// TestBraveLocal_DistanceRank asserts F2 distance-ranking: with an anchor, the
// nearer POI sorts first regardless of Brave's return order.
func TestBraveLocal_DistanceRank(t *testing.T) {
	t.Parallel()
	var caps localHeaders
	b, closeFn := newBraveTestProvider(t, BraveConfig{}, braveLocalServer(t, &caps))
	defer closeFn()

	lat, lon := 47.6062, -122.3321
	results, err := b.Local(context.Background(), LocalSearchParams{
		Query: "coffee", Latitude: &lat, Longitude: &lon, NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Name != "Near Cafe" {
		t.Errorf("nearest-first: results[0] = %q, want %q", results[0].Name, "Near Cafe")
	}
}

// TestBraveLocal_RadiusFilter asserts F2 radius filtering: a tight radius drops
// the distant POI entirely.
func TestBraveLocal_RadiusFilter(t *testing.T) {
	t.Parallel()
	var caps localHeaders
	b, closeFn := newBraveTestProvider(t, BraveConfig{}, braveLocalServer(t, &caps))
	defer closeFn()

	lat, lon := 47.6062, -122.3321
	results, err := b.Local(context.Background(), LocalSearchParams{
		Query: "coffee", Latitude: &lat, Longitude: &lon, Radius: 5000, NumResults: 5, // 5km
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result within 5km, got %d", len(results))
	}
	if results[0].Name != "Near Cafe" {
		t.Errorf("kept POI = %q, want %q", results[0].Name, "Near Cafe")
	}
}

// TestBraveLocal_BreakerOpensAfterFailures asserts F3: step-1 failures count
// against the breaker, and once it trips, further calls fail fast without
// hitting the network.
func TestBraveLocal_BreakerOpensAfterFailures(t *testing.T) {
	t.Parallel()
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 2, ResetTimeout: 60})}
	b := NewBraveProvider("brave-key", BraveConfig{}, deps)

	// Two failures trip the breaker (threshold 2).
	for i := 0; i < 2; i++ {
		if _, err := b.Local(context.Background(), LocalSearchParams{Query: "coffee"}); err == nil {
			t.Fatalf("call %d: expected error", i)
		}
	}
	hitsAtTrip := hits
	// Subsequent call must short-circuit (no new network hit).
	if _, err := b.Local(context.Background(), LocalSearchParams{Query: "coffee"}); err == nil {
		t.Fatal("expected breaker-open error")
	}
	if hits != hitsAtTrip {
		t.Errorf("breaker did not short-circuit: hits went %d → %d", hitsAtTrip, hits)
	}
}

// --- D. Images & News contract ------------------------------------------------

// TestBraveImages_RequestContract asserts F6: count cap (200), safesearch
// mapped to off|strict only, country/search_lang sent, and the Google-only
// filter fields (size/type/color_type/dominant_color/file_type) are NEVER sent
// to Brave even when populated.
func TestBraveImages_RequestContract(t *testing.T) {
	t.Parallel()
	var got url.Values
	b, closeFn := newBraveTestProvider(t, BraveConfig{}, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	defer closeFn()

	_, err := b.Images(context.Background(), ImageSearchParams{
		Query: "cats", NumResults: 500, // over the 200 cap
		Safe: "moderate", Country: "fr", Language: "fr",
		Size: "large", Type: "photo", ColorType: "color", DominantColor: "red", FileType: "png",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Get("count") != "200" {
		t.Errorf("count = %q, want 200 (capped)", got.Get("count"))
	}
	// Images have no "moderate" — any non-off level maps to strict.
	if got.Get("safesearch") != "strict" {
		t.Errorf("safesearch = %q, want strict", got.Get("safesearch"))
	}
	if got.Get("country") != "fr" {
		t.Errorf("country = %q, want fr", got.Get("country"))
	}
	if got.Get("search_lang") != "fr" {
		t.Errorf("search_lang = %q, want fr", got.Get("search_lang"))
	}
	for _, banned := range []string{"size", "type", "color_type", "dominant_color", "file_type", "imgSize", "imgType"} {
		if got.Get(banned) != "" {
			t.Errorf("Google-only param %q must not be sent to Brave, got %q", banned, got.Get(banned))
		}
	}
}

// TestBraveImages_SafeSearchOffPreserved asserts that an explicit off level is
// preserved (not forced to strict) and an empty level omits the param.
func TestBraveImages_SafeSearchOffPreserved(t *testing.T) {
	t.Parallel()
	tests := []struct {
		safe string
		want string
	}{
		{"", ""},
		{"off", "off"},
		{"strict", "strict"},
		{"moderate", "strict"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.safe, func(t *testing.T) {
			t.Parallel()
			var got url.Values
			b, closeFn := newBraveTestProvider(t, BraveConfig{}, func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"results":[]}`))
			})
			defer closeFn()
			if _, err := b.Images(context.Background(), ImageSearchParams{Query: "q", NumResults: 5, Safe: tt.safe}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Get("safesearch") != tt.want {
				t.Errorf("safe=%q → safesearch=%q, want %q", tt.safe, got.Get("safesearch"), tt.want)
			}
		})
	}
}

// TestBraveNews_RequestContract asserts F7: count cap (50), country/search_lang
// sent, safesearch mapped (full off|moderate|strict set), offset clamped 0–9,
// and the Google-only sort_by/news_source fields are never sent to Brave.
func TestBraveNews_RequestContract(t *testing.T) {
	t.Parallel()
	var got url.Values
	b, closeFn := newBraveTestProvider(t, BraveConfig{}, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	defer closeFn()

	_, err := b.News(context.Background(), NewsSearchParams{
		Query: "election", NumResults: 100, // over the 50 cap
		Country: "gb", Language: "en", Safe: "moderate", Offset: 50,
		SortBy: "date", Source: "reuters.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Get("count") != "50" {
		t.Errorf("count = %q, want 50 (capped)", got.Get("count"))
	}
	if got.Get("country") != "gb" {
		t.Errorf("country = %q, want gb", got.Get("country"))
	}
	if got.Get("search_lang") != "en" {
		t.Errorf("search_lang = %q, want en", got.Get("search_lang"))
	}
	if got.Get("safesearch") != "moderate" {
		t.Errorf("safesearch = %q, want moderate", got.Get("safesearch"))
	}
	if got.Get("offset") != "9" {
		t.Errorf("offset = %q, want 9 (clamped)", got.Get("offset"))
	}
	for _, banned := range []string{"sort_by", "sort", "news_source", "source"} {
		if got.Get(banned) != "" {
			t.Errorf("Google-only param %q must not be sent to Brave, got %q", banned, got.Get(banned))
		}
	}
}

// --- E. Error envelope --------------------------------------------------------

// TestBraveError_QuotaVsRateLimit asserts F10: the structured error envelope
// distinguishes monthly-quota exhaustion from a per-second throttle, while both
// retain a token isRateLimitError keys on ("rate limited"/"429"/"quota").
func TestBraveError_QuotaVsRateLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      int
		body        string
		wantSubstrs []string // all must be present
	}{
		{
			name:   "monthly quota exhausted",
			status: 429,
			body: `{"type":"ErrorResponse","error":{"code":"RATE_LIMITED","detail":"quota",
				"meta":{"plan":"Free","quota_limit":2000,"quota_current":2000}}}`,
			wantSubstrs: []string{"quota", "rate limited", "Free"},
		},
		{
			name:   "per-second throttle",
			status: 429,
			body: `{"type":"ErrorResponse","error":{"code":"RATE_LIMITED","detail":"slow down",
				"meta":{"plan":"Pro","component":"rate_limiter","rate_limit":20,"rate_current":21}}}`,
			wantSubstrs: []string{"rate limited", "per second", "rate_limiter"},
		},
		{
			name:        "bare 429 no envelope",
			status:      429,
			body:        `gateway throttled`,
			wantSubstrs: []string{"rate limited"},
		},
		{
			name:        "generic non-rate error",
			status:      400,
			body:        `{"type":"ErrorResponse","error":{"code":"VALIDATION","detail":"bad query"}}`,
			wantSubstrs: []string{"400", "VALIDATION", "bad query"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := braveError(tt.status, []byte(tt.body))
			if err == nil {
				t.Fatal("expected an error")
			}
			msg := err.Error()
			for _, sub := range tt.wantSubstrs {
				if !strings.Contains(msg, sub) {
					t.Errorf("error %q missing substring %q", msg, sub)
				}
			}
		})
	}
}

// TestBraveError_RateLimitTokenSurvives asserts the coupling the tools layer
// relies on (internal/tools isRateLimitError): every RATE_LIMITED / 429 message,
// however enriched, still contains a token classifiable as rate-limited.
func TestBraveError_RateLimitTokenSurvives(t *testing.T) {
	t.Parallel()
	bodies := []string{
		`{"error":{"code":"RATE_LIMITED","meta":{"quota_limit":10,"quota_current":10,"plan":"Free"}}}`,
		`{"error":{"code":"RATE_LIMITED","meta":{"component":"rl","rate_limit":1,"rate_current":2}}}`,
		`{"error":{"code":"RATE_LIMITED"}}`,
		`anything`,
	}
	rateLike := func(s string) bool {
		s = strings.ToLower(s)
		return strings.Contains(s, "rate limited") || strings.Contains(s, "429") || strings.Contains(s, "quota")
	}
	// status 429 must always classify as rate-limited.
	for _, body := range bodies {
		if msg := braveError(429, []byte(body)).Error(); !rateLike(msg) {
			t.Errorf("429 message not rate-classifiable: %q", msg)
		}
	}
	// RATE_LIMITED code on a non-429 status must also classify.
	codeBody := `{"error":{"code":"RATE_LIMITED"}}`
	if msg := braveError(503, []byte(codeBody)).Error(); !rateLike(msg) {
		t.Errorf("RATE_LIMITED code not rate-classifiable: %q", msg)
	}
}

// TestBraveDoRequest_GzipErrorBody asserts F4/F10: a gzip-encoded error body is
// decompressed BEFORE the status check, so braveError parses Brave's structured
// envelope instead of choking on raw binary. Without the early decode, a 429's
// quota/rate detail would be lost.
func TestBraveDoRequest_GzipErrorBody(t *testing.T) {
	t.Parallel()
	envelope := `{"type":"ErrorResponse","error":{"code":"RATE_LIMITED","detail":"quota","meta":{"plan":"Free","quota_limit":2000,"quota_current":2000}}}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusTooManyRequests)
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte(envelope))
		_ = gz.Close()
	}))
	defer ts.Close()
	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("brave-key", BraveConfig{}, deps)

	_, err := b.doRequest(context.Background(), "https://api.search.brave.com/res/v1/web/search?q=x")
	if err == nil {
		t.Fatal("expected an error from a 429 response")
	}
	msg := err.Error()
	// The gzip body was decoded and parsed: quota detail is present, not binary.
	for _, sub := range []string{"quota", "rate limited", "Free"} {
		if !strings.Contains(msg, sub) {
			t.Errorf("decoded error %q missing %q (gzip body not decoded before status check?)", msg, sub)
		}
	}
}

// TestBraveWeb_NoCacheOptIn asserts withNoCache sets Cache-Control: no-cache and
// is off by default (F10) — the seam is opt-in, not always-on.
func TestBraveWeb_NoCacheOptIn(t *testing.T) {
	t.Parallel()
	var gotCacheControl string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCacheControl = r.Header.Get("Cache-Control")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	defer ts.Close()
	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("brave-key", BraveConfig{}, deps)

	// Default web search must NOT send Cache-Control.
	if _, err := b.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCacheControl != "" {
		t.Errorf("default call should not set Cache-Control, got %q", gotCacheControl)
	}

	// An explicit withNoCache opt-in sets the header.
	if _, err := b.doRequest(context.Background(), "https://api.search.brave.com/res/v1/web/search?q=x", withNoCache()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCacheControl != "no-cache" {
		t.Errorf("withNoCache: Cache-Control = %q, want no-cache", gotCacheControl)
	}
}

// --- unit: haversine + safesearch mappers ------------------------------------

// TestHaversineMeters checks the great-circle distance against a known value
// (1° of latitude ≈ 111.2 km) within a small tolerance.
func TestHaversineMeters(t *testing.T) {
	t.Parallel()
	d := haversineMeters(0, 0, 1, 0)
	const want = 111195.0 // meters per degree of latitude on a 6371km sphere
	if d < want-500 || d > want+500 {
		t.Errorf("haversineMeters(0,0,1,0) = %.0f, want ≈%.0f", d, want)
	}
	if got := haversineMeters(47.6062, -122.3321, 47.6062, -122.3321); got != 0 {
		t.Errorf("identical points = %.6f, want 0", got)
	}
}

// TestRankByHaversine_NoGeoSortsLast asserts coordinate-less POIs sort after
// geo-bearing ones, and are excluded entirely under a radius filter.
func TestRankByHaversine_NoGeoSortsLast(t *testing.T) {
	t.Parallel()
	in := []LocalResult{
		{Name: "no-geo"}, // Lat==0 && Lon==0
		{Name: "far", Lat: 48.05, Lon: -122.3321},   // ~50km
		{Name: "near", Lat: 47.615, Lon: -122.3321}, // ~1km
	}
	// No radius: near, far, then the unrankable no-geo last.
	out := rankByHaversine(in, 47.6062, -122.3321, 0)
	gotOrder := []string{out[0].Name, out[1].Name, out[2].Name}
	wantOrder := []string{"near", "far", "no-geo"}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Errorf("order[%d] = %q, want %q (full %v)", i, gotOrder[i], wantOrder[i], gotOrder)
		}
	}
	// With a 5km radius: only "near" survives; no-geo is excluded (unprovable).
	filtered := rankByHaversine(in, 47.6062, -122.3321, 5000)
	if len(filtered) != 1 || filtered[0].Name != "near" {
		t.Errorf("radius-filtered = %v, want [near]", filtered)
	}
}

// TestClampLocalCount checks the 1–20 normalization with a default of 5.
func TestClampLocalCount(t *testing.T) {
	t.Parallel()
	cases := map[int]int{-1: 5, 0: 5, 1: 1, 5: 5, 20: 20, 100: 20}
	for in, want := range cases {
		if got := clampLocalCount(in); got != want {
			t.Errorf("clampLocalCount(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestBraveImageResponse_Decode is a small fixture-parse guard so a future
// response-shape edit that breaks field mapping is caught.
func TestBraveImageResponse_Decode(t *testing.T) {
	t.Parallel()
	var resp braveImageResponse
	body := `{"results":[{"title":"Cat","url":"https://img/1.jpg","source":"example.com","thumbnail":{"src":"https://thumb/1.jpg"}}]}`
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Thumbnail.Src != "https://thumb/1.jpg" {
		t.Errorf("unexpected decode: %+v", resp.Results)
	}
}
