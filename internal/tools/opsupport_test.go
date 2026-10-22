package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// opsMock is a named provider with a declared operation set. Images and News
// return a result only when declared, like the real providers do.
type opsMock struct {
	name string
	ops  []search.Operation
}

func (m *opsMock) Web(context.Context, search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{{Title: "w", URL: "https://example.com"}}, nil
}
func (m *opsMock) Images(context.Context, search.ImageSearchParams) ([]search.ImageResult, error) {
	if !search.Supports(m, search.OpImages) {
		return nil, nil
	}
	return []search.ImageResult{{Title: "i", Link: "https://example.com/i.png", DisplayLink: "example.com"}}, nil
}
func (m *opsMock) News(context.Context, search.NewsSearchParams) ([]search.NewsResult, error) {
	if !search.Supports(m, search.OpNews) {
		return nil, nil
	}
	return []search.NewsResult{{Title: "n", URL: "https://example.com/n", Source: "s"}}, nil
}
func (m *opsMock) Name() string                            { return m.name }
func (m *opsMock) SupportedOperations() []search.Operation { return m.ops }

var (
	webOnlyOps = []search.Operation{search.OpWeb}
	allOps     = []search.Operation{search.OpWeb, search.OpImages, search.OpNews}
)

func callOps(t *testing.T, deps Dependencies, tool string, args map[string]any) (string, bool) {
	t.Helper()
	ctx := context.Background()
	session := connectTestClient(ctx, t, createTestServer(deps))
	defer session.Close()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

func opsDeps(def search.Provider, others ...search.Provider) Dependencies {
	deps := setupTestDeps()
	deps.Search = def
	deps.SearchProviders = map[string]search.Provider{def.Name(): def}
	for _, o := range others {
		deps.SearchProviders[o.Name()] = o
	}
	return deps
}

func TestExplicitUnsupportedProviderIsAnError(t *testing.T) {
	for tool, label := range map[string]string{"image_search": "image", "news_search": "news"} {
		t.Run(tool, func(t *testing.T) {
			stub := &opsMock{name: "duckduckgo", ops: webOnlyOps}
			good := &opsMock{name: "brave", ops: allOps}
			text, isErr := callOps(t, opsDeps(good, stub), tool, map[string]any{"query": "cat", "provider": "duckduckgo"})
			if !isErr {
				t.Fatalf("want an error, got %s", text)
			}
			for _, want := range []string{`"duckduckgo" does not support ` + label + ` search`, `"kind":"config"`, `"alternatives":["brave"]`} {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q in %s", want, text)
				}
			}
		})
	}
}

func TestUnsupportedDefaultSwitchesToCapableProvider(t *testing.T) {
	for tool, label := range map[string]string{"image_search": "image", "news_search": "news"} {
		t.Run(tool, func(t *testing.T) {
			stub := &opsMock{name: "duckduckgo", ops: webOnlyOps}
			// serper ranks after brave in search.SupportedProviders.
			deps := opsDeps(stub, &opsMock{name: "serper", ops: allOps}, &opsMock{name: "brave", ops: allOps})
			text, isErr := callOps(t, deps, tool, map[string]any{"query": "cat"})
			if isErr {
				t.Fatalf("unexpected error: %s", text)
			}
			want := `The default provider \"duckduckgo\" does not support ` + label + ` search; results come from \"brave\".`
			if !strings.Contains(text, want) {
				t.Errorf("missing substitution warning %q in %s", want, text)
			}
			if !strings.Contains(text, `"resultCount":1`) {
				t.Errorf("want one result from the capable provider: %s", text)
			}
		})
	}
}

func TestUnsupportedDefaultWithNoCapableProviderIsAnError(t *testing.T) {
	for _, tool := range []string{"image_search", "news_search"} {
		t.Run(tool, func(t *testing.T) {
			stub := &opsMock{name: "duckduckgo", ops: webOnlyOps}
			text, isErr := callOps(t, opsDeps(stub, &opsMock{name: "bluesky", ops: webOnlyOps}), tool, map[string]any{"query": "cat"})
			if !isErr || !strings.Contains(text, "docs/PROVIDERS.md") || strings.Contains(text, `"alternatives"`) {
				t.Errorf("want a config error without alternatives pointing at the matrix, got isErr=%v %s", isErr, text)
			}
		})
	}
}

func TestRouterWithNoCapableProviderIsAnError(t *testing.T) {
	stub := &opsMock{name: "duckduckgo", ops: webOnlyOps}
	other := &opsMock{name: "bluesky", ops: webOnlyOps}
	deps := opsDeps(stub, other)
	deps.Search = search.NewRouter(map[string]search.Provider{"duckduckgo": stub, "bluesky": other}, search.RouterConfig{})
	// The router reports web only, so the tool rejects before routing.
	text, isErr := callOps(t, deps, "image_search", map[string]any{"query": "cat"})
	if !isErr || !strings.Contains(text, "no configured provider supports image search") {
		t.Errorf("want unsupported-operation error, got isErr=%v %s", isErr, text)
	}
}

func TestRouterRoutingExcludingCapableProvidersIsAnError(t *testing.T) {
	stub := &opsMock{name: "duckduckgo", ops: webOnlyOps}
	good := &opsMock{name: "brave", ops: allOps}
	deps := opsDeps(stub, good)
	deps.Search = search.NewRouter(map[string]search.Provider{"duckduckgo": stub, "brave": good},
		search.RouterConfig{Routing: search.RoutingConfig{News: []string{"duckduckgo"}}})
	text, isErr := callOps(t, deps, "news_search", map[string]any{"query": "cat"})
	if !isErr || !strings.Contains(text, "no configured provider supports news search") || !strings.Contains(text, `"alternatives":["brave"]`) {
		t.Errorf("want unsupported-operation error naming brave, got isErr=%v %s", isErr, text)
	}
}

func TestImageZeroResultHintsSuggestOnlyImageCapableAlternatives(t *testing.T) {
	empty := &emptyImageMock{opsMock{name: "google", ops: allOps}}
	deps := opsDeps(empty,
		&opsMock{name: "brave", ops: allOps},
		&opsMock{name: "duckduckgo", ops: webOnlyOps})
	text, isErr := callOps(t, deps, "image_search", map[string]any{"query": "cat"})
	if isErr {
		t.Fatal(text)
	}
	if !strings.Contains(text, `"brave"`) || strings.Contains(text, `"duckduckgo"`) {
		t.Errorf("hints should name brave and not duckduckgo: %s", text)
	}
}

type emptyImageMock struct{ opsMock }

func (*emptyImageMock) Images(context.Context, search.ImageSearchParams) ([]search.ImageResult, error) {
	return []search.ImageResult{}, nil
}
