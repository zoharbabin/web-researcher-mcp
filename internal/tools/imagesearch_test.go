package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// TestManyImageFiltersWarning is the #659 regression test: Google's Custom
// Search API may silently relax 3+ combined image filters and fall back to
// broader/default ranking with no signal in the response — the tool should
// surface a warning rather than pretend the request was honored as-is.
func TestManyImageFiltersWarning(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		params       search.ImageSearchParams
		providerUsed string
		wantWarning  bool
	}{
		{
			name:         "google with 3 filters set warns",
			params:       search.ImageSearchParams{Size: "large", Type: "clipart", ColorType: "trans"},
			providerUsed: "google",
			wantWarning:  true,
		},
		{
			name:         "google with all 5 filters set warns",
			params:       search.ImageSearchParams{Size: "large", Type: "clipart", ColorType: "trans", DominantColor: "blue", FileType: "png"},
			providerUsed: "google",
			wantWarning:  true,
		},
		{
			name:         "google with only 1 filter set does not warn",
			params:       search.ImageSearchParams{Type: "clipart"},
			providerUsed: "google",
			wantWarning:  false,
		},
		{
			name:         "google with 2 filters set does not warn",
			params:       search.ImageSearchParams{Type: "clipart", Size: "large"},
			providerUsed: "google",
			wantWarning:  false,
		},
		{
			name:         "non-google provider with all 5 filters set does not warn",
			params:       search.ImageSearchParams{Size: "large", Type: "clipart", ColorType: "trans", DominantColor: "blue", FileType: "png"},
			providerUsed: "brave",
			wantWarning:  false,
		},
		{
			name:         "no filters set does not warn",
			params:       search.ImageSearchParams{},
			providerUsed: "google",
			wantWarning:  false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := manyImageFiltersWarning(c.params, c.providerUsed)
			if c.wantWarning && got == "" {
				t.Errorf("expected a non-empty warning, got empty")
			}
			if !c.wantWarning && got != "" {
				t.Errorf("expected no warning, got %q", got)
			}
		})
	}
}

// googleNamedImageProvider is a mock provider whose Name() returns "google" so
// integration tests can exercise the image_search tool's provider-conditional
// warning wiring end-to-end.
type googleNamedImageProvider struct{}

func (p *googleNamedImageProvider) Web(_ context.Context, _ search.WebSearchParams) ([]search.SearchResult, error) {
	return nil, nil
}
func (p *googleNamedImageProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return []search.ImageResult{
		{Title: "Test Image", Link: "https://example.com/img.png", DisplayLink: "example.com"},
	}, nil
}
func (p *googleNamedImageProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, nil
}
func (p *googleNamedImageProvider) Name() string { return "google" }

func TestImageSearchToolSurfacesManyFiltersWarning(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &googleNamedImageProvider{}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "image_search",
		Arguments: map[string]any{
			"query":          "cat",
			"size":           "large",
			"type":           "clipart",
			"color_type":     "trans",
			"dominant_color": "blue",
			"file_type":      "png",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output struct {
		Warning string `json:"warning"`
	}
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output.Warning == "" {
		t.Fatal("expected a warning when 3+ image filters are combined against google, got none")
	}
}

func TestImageSearchToolNoWarningWithFewFilters(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &googleNamedImageProvider{}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "image_search",
		Arguments: map[string]any{
			"query": "cat",
			"type":  "clipart",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output struct {
		Warning string `json:"warning"`
	}
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output.Warning != "" {
		t.Fatalf("expected no warning with only 1 filter set, got %q", output.Warning)
	}
}
