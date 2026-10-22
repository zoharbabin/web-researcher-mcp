package search

import (
	"context"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
)

// fullSearchConfig has a credential for every provider, so NewProviderByName
// builds all of them.
func fullSearchConfig() config.SearchConfig {
	return config.SearchConfig{
		GoogleAPIKey: "k", GoogleCX: "cx", BraveAPIKey: "k", SerperAPIKey: "k",
		SearXNGURL: "http://127.0.0.1:1", SearchAPIKey: "k", YouDotComAPIKey: "k",
		TavilyAPIKey: "k", ExaAPIKey: "k", GitHubToken: "k", XQuikAPIKey: "k", ParallelEnabled: true,
	}
}

// countingTransport records every outbound request and fails it.
type countingTransport struct{ calls atomic.Int32 }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return nil, errors.New("blocked by test")
}

func allProviders(t *testing.T, client *http.Client) map[string]Provider {
	t.Helper()
	out := map[string]Provider{}
	for _, name := range SupportedProviders {
		p := NewProviderByName(name, fullSearchConfig(), Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{})})
		if p == nil {
			t.Fatalf("provider %q could not be built with the full test config", name)
		}
		out[name] = p
	}
	return out
}

func TestSupports(t *testing.T) {
	t.Parallel()
	if !Supports(&successProvider{name: "x"}, OpImages) {
		t.Error("a provider without a declaration is assumed to support everything")
	}
	ddg := &DuckDuckGoProvider{}
	if !Supports(ddg, OpWeb) || Supports(ddg, OpImages) || Supports(ddg, OpNews) {
		t.Error("DuckDuckGo serves web only")
	}
	brave := &BraveProvider{}
	for _, op := range []Operation{OpWeb, OpImages, OpNews} {
		if !Supports(brave, op) {
			t.Errorf("Brave must support %s", op)
		}
	}
}

// TestEveryProviderDeclaresOperations is the guard for new providers: each one
// in SupportedProviders must declare its operations, and the declaration must
// match what the code does. A stubbed operation makes no request and returns
// (nil, nil). A declared one reaches the network.
func TestEveryProviderDeclaresOperations(t *testing.T) {
	t.Parallel()
	for name, p := range allProviders(t, &http.Client{Transport: &countingTransport{}}) {
		if _, ok := p.(OperationSupport); !ok {
			t.Errorf("%s does not implement OperationSupport", name)
			continue
		}
		if !Supports(p, OpWeb) {
			t.Errorf("%s must declare web support", name)
		}
	}
	for _, op := range []Operation{OpImages, OpNews} {
		for _, name := range SupportedProviders {
			t.Run(string(op)+"/"+name, func(t *testing.T) {
				t.Parallel()
				tr := &countingTransport{}
				p := NewProviderByName(name, fullSearchConfig(), Deps{HTTPClient: &http.Client{Transport: tr}, Breaker: circuit.New(circuit.Config{})})
				ctx := context.Background()
				var err error
				var res int
				if op == OpImages {
					r, e := p.Images(ctx, ImageSearchParams{Query: "cat", NumResults: 3})
					res, err = len(r), e
				} else {
					r, e := p.News(ctx, NewsSearchParams{Query: "cat", NumResults: 3, Freshness: "week"})
					res, err = len(r), e
				}
				if Supports(p, op) {
					if tr.calls.Load() == 0 {
						t.Errorf("%s declares %s support but made no request", name, op)
					}
					return
				}
				if tr.calls.Load() != 0 || err != nil || res != 0 {
					t.Errorf("%s stubs %s: want no request and (nil, nil), got %d calls, %d results, err=%v",
						name, op, tr.calls.Load(), res, err)
				}
			})
		}
	}
}

// TestProvidersDocCapabilityMatrix keeps the docs/PROVIDERS.md capability
// matrix (web/image/news columns) in step with the declarations.
func TestProvidersDocCapabilityMatrix(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../docs/PROVIDERS.md")
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{
		"DuckDuckGo": "duckduckgo", "Google PSE": "google", "Serper": "serper",
		"SearchAPI.io": "searchapi", "You.com": "youcom", "Brave": "brave", "Exa": "exa",
		"Tavily": "tavily", "SearXNG": "searxng", "HackerNews": "hackernews",
		"Reddit": "reddit", "Bluesky": "bluesky", "GitHub": "github", "Xquik": "xquik", "Parallel": "parallel",
	}
	providers := allProviders(t, &http.Client{})
	rowRE := regexp.MustCompile(`^\| \*\*\[([^\]]+)\]`)
	seen := map[string]bool{}
	inMatrix := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "| Provider | `web_search` | `image_search` | `news_search`") {
			inMatrix = true
			continue
		}
		if !inMatrix {
			continue
		}
		m := rowRE.FindStringSubmatch(line)
		if m == nil {
			if strings.HasPrefix(line, "|") {
				continue
			}
			break
		}
		key, ok := labels[m[1]]
		if !ok {
			t.Errorf("matrix row %q has no entry in the test label map", m[1])
			continue
		}
		cells := strings.Split(line, "|")
		seen[key] = true
		for i, op := range []Operation{OpWeb, OpImages, OpNews} {
			doc := strings.Contains(cells[3+i-1], "✓")
			if got := Supports(providers[key], op); got != doc {
				t.Errorf("docs/PROVIDERS.md says %s %s=%v, code declares %v", key, op, doc, got)
			}
		}
	}
	for _, name := range SupportedProviders {
		if !seen[name] {
			t.Errorf("provider %q is missing from the docs/PROVIDERS.md capability matrix", name)
		}
	}
}

// stubProvider serves web and stubs Images/News, like DuckDuckGo.
type stubProvider struct{ successProvider }

func (*stubProvider) SupportedOperations() []Operation { return opsWeb }
func (*stubProvider) Images(context.Context, ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}
func (*stubProvider) News(context.Context, NewsSearchParams) ([]NewsResult, error) {
	return nil, nil
}

func TestRouterSkipsProvidersThatCannotServeOperation(t *testing.T) {
	t.Parallel()
	r := NewRouter(map[string]Provider{
		"stub": &stubProvider{successProvider{name: "stub"}},
		"real": &successProvider{name: "real"},
	}, RouterConfig{Routing: RoutingConfig{Default: []string{"stub", "real"}}})

	ctx, trace := NewRoutingTrace(context.Background())
	imgs, err := r.Images(ctx, ImageSearchParams{Query: "q"})
	if err != nil || len(imgs) != 1 {
		t.Fatalf("want the capable provider's image, got %v, %v", imgs, err)
	}
	if d := trace.Decision(); d.ProviderUsed != "real" || d.Fallback || len(d.Attempted) != 1 {
		t.Errorf("stub must be skipped silently, not attempted: %+v", d)
	}
	news, err := r.News(context.Background(), NewsSearchParams{Query: "q"})
	if err != nil || len(news) != 1 {
		t.Fatalf("want the capable provider's news, got %v, %v", news, err)
	}
}

func TestRouterErrorsWhenNoProviderServesOperation(t *testing.T) {
	t.Parallel()
	r := NewRouter(map[string]Provider{
		"stub": &stubProvider{successProvider{name: "stub"}},
	}, RouterConfig{})
	var ue *UnsupportedOperationError
	if _, err := r.Images(context.Background(), ImageSearchParams{Query: "q"}); !errors.As(err, &ue) || ue.Op != OpImages {
		t.Errorf("images: want UnsupportedOperationError, got %v", err)
	}
	if _, err := r.News(context.Background(), NewsSearchParams{Query: "q"}); !errors.As(err, &ue) || ue.Op != OpNews {
		t.Errorf("news: want UnsupportedOperationError, got %v", err)
	}
	if _, err := r.Web(context.Background(), WebSearchParams{Query: "q"}); err != nil {
		t.Errorf("web must still work: %v", err)
	}
	if Supports(r, OpImages) || Supports(r, OpNews) || !Supports(r, OpWeb) {
		t.Errorf("router must report web only, got %v", r.SupportedOperations())
	}
}

// A routing list that names only incapable providers does not fall through to
// capable providers outside the list: the operator's list is exclusive.
func TestRouterHonorsExclusiveRoutingForUnsupportedOperation(t *testing.T) {
	t.Parallel()
	r := NewRouter(map[string]Provider{
		"stub": &stubProvider{successProvider{name: "stub"}},
		"real": &successProvider{name: "real"},
	}, RouterConfig{Routing: RoutingConfig{Images: []string{"stub"}}})
	var ue *UnsupportedOperationError
	if _, err := r.Images(context.Background(), ImageSearchParams{Query: "q"}); !errors.As(err, &ue) {
		t.Errorf("want UnsupportedOperationError, got %v", err)
	}
	if !Supports(r, OpImages) {
		t.Error("router-level support is the union of providers; routing is checked per call")
	}
}

func TestRouterDefaultOrderIsDeterministic(t *testing.T) {
	t.Parallel()
	ps := map[string]Provider{}
	for _, n := range []string{"c", "a", "d", "b"} {
		ps[n] = &successProvider{name: n}
	}
	r := NewRouter(ps, RouterConfig{})
	for i := 0; i < 20; i++ {
		if got := strings.Join(r.priorityFor(OpWeb), ","); got != "a,b,c,d" {
			t.Fatalf("default priority = %s, want a,b,c,d", got)
		}
	}
}
