package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// mixedNewsProvider returns a fixed mix of social-media and outlet results, so
// tests can assert news_search's per-article source-type classification (#524)
// without depending on a live provider.
type mixedNewsProvider struct{}

func (m *mixedNewsProvider) Web(_ context.Context, _ search.WebSearchParams) ([]search.SearchResult, error) {
	return nil, nil
}
func (m *mixedNewsProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, nil
}
func (m *mixedNewsProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return []search.NewsResult{
		{Title: "Fed holds rates", URL: "https://www.reuters.com/markets/fed-holds-rates", Source: "Reuters"},
		{Title: "Fed post", URL: "https://www.facebook.com/21WFMJ/posts/federal-reserve-keeps-interest-rates-unchanged", Source: "www.facebook.com"},
		{Title: "Fed clip", URL: "https://www.instagram.com/p/DbkmBryDYBJ/", Source: "www.instagram.com"},
	}, nil
}
func (m *mixedNewsProvider) Name() string { return "mixed" }

func TestNewsResultsSourceTypeSocialMedia(t *testing.T) {
	articles := classifyNewsResults([]search.NewsResult{
		{Title: "Fed post", URL: "https://www.facebook.com/21WFMJ/posts/federal-reserve-keeps-interest-rates-unchanged", Source: "www.facebook.com"},
		{Title: "Fed clip", URL: "https://www.instagram.com/p/DbkmBryDYBJ/", Source: "www.instagram.com"},
	})
	for _, a := range articles {
		if !a.IsSocialMedia {
			t.Errorf("expected %s to be flagged isSocialMedia, got false", a.URL)
		}
		if a.SourceType != "social_media" {
			t.Errorf("expected %s sourceType=social_media, got %q", a.URL, a.SourceType)
		}
	}
}

func TestNewsResultsSourceTypeRegularOutlet(t *testing.T) {
	articles := classifyNewsResults([]search.NewsResult{
		{Title: "Fed holds rates", URL: "https://www.reuters.com/markets/fed-holds-rates", Source: "Reuters"},
	})
	if len(articles) != 1 {
		t.Fatalf("expected 1 article, got %d", len(articles))
	}
	if articles[0].IsSocialMedia {
		t.Errorf("reuters.com must not be flagged isSocialMedia")
	}
	if articles[0].SourceType != "news_publication" {
		t.Errorf("expected sourceType=news_publication for reuters.com, got %q", articles[0].SourceType)
	}
}

func TestNewsSearchToolSourceType(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mixedNewsProvider{}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "news_search",
		Arguments: map[string]any{"query": "Federal Reserve interest rate decision", "time_range": "day", "num_results": float64(10)},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output struct {
		Articles []struct {
			URL           string `json:"url"`
			SourceType    string `json:"sourceType"`
			IsSocialMedia bool   `json:"isSocialMedia"`
		} `json:"articles"`
	}
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if len(output.Articles) != 3 {
		t.Fatalf("expected 3 articles, got %d", len(output.Articles))
	}

	socialCount := 0
	for _, a := range output.Articles {
		if a.IsSocialMedia {
			socialCount++
			if a.SourceType != "social_media" {
				t.Errorf("article %s: isSocialMedia=true but sourceType=%q", a.URL, a.SourceType)
			}
		}
	}
	if socialCount != 2 {
		t.Errorf("expected 2 social-media articles flagged, got %d", socialCount)
	}
}
