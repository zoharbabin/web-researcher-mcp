package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// EcosystemsAwesomeProvider implements AwesomeListSearcher over the
// ecosyste.ms Awesome API: a structured index of community-curated
// "awesome-*" lists (GitHub topic → curated repositories), each carrying
// stargazer count, curated-entry count, topics, and last-sync freshness.
// Keyless and free at the shared "common" pool. An optional contact email
// opts into the "polite" pool (verified: 5,000 → 15,000 req/period) — see
// ecosyste.ms/api. An optional API key is also sent, but per ecosyste.ms's
// published pricing (ecosyste.ms/pricing) key-based auth only takes effect
// on the paid Develop/Scale plans, not the Free plan self-service keys are
// issued under — it's a no-op today, kept for forward compatibility.
//
// Verified contract (2026):
//   - topic:  GET /api/v1/topics/{slug}
//     → {slug, name, short_description, github_count, related_topics, …}
//     404 when the slug isn't a known topic.
//   - lists:  GET /api/v1/lists?topic={slug}&per_page=N
//     → [{id, url, name, description, projects_count, last_synced_at,
//     repository:{full_name, archived, stargazers_count, topics, …}}, …]
//     no-match is a 200 with an empty array.
type EcosystemsAwesomeProvider struct {
	apiKey  string
	email   string
	baseURL string
	deps    Deps
}

// NewEcosystemsAwesomeProvider creates the provider. apiKey and email may
// both be "" (keyless — works at the shared "common" rate limit).
func NewEcosystemsAwesomeProvider(apiKey, email string, deps Deps) *EcosystemsAwesomeProvider {
	return &EcosystemsAwesomeProvider{
		apiKey:  apiKey,
		email:   email,
		baseURL: "https://awesome.ecosyste.ms/api/v1",
		deps:    deps,
	}
}

func (e *EcosystemsAwesomeProvider) Name() string { return "ecosystems" }

func (e *EcosystemsAwesomeProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "awesome-lists", "curated"},
		RateClass:    "free",
		Description:  "ecosyste.ms Awesome API — community-curated awesome-* lists by GitHub topic, with stars, curated-entry count, and freshness",
	}
}

// SetBaseURL overrides the API base URL (testing).
func (e *EcosystemsAwesomeProvider) SetBaseURL(base string) { e.baseURL = base }

func (e *EcosystemsAwesomeProvider) AwesomeLists(ctx context.Context, params AwesomeListSearchParams) ([]AwesomeListResult, error) {
	var results []AwesomeListResult
	err := e.deps.Breaker.Execute(func() error {
		var er error
		results, er = e.doSearch(ctx, params)
		return er
	})
	return results, err
}

func (e *EcosystemsAwesomeProvider) doSearch(ctx context.Context, params AwesomeListSearchParams) ([]AwesomeListResult, error) {
	num := clamp(params.NumResults, 1, 100)

	topic := params.Topic
	if topic == "" {
		topic = params.Query
	}
	if topic == "" {
		return nil, fmt.Errorf("ecosystems: topic or query is required")
	}

	q := url.Values{}
	q.Set("topic", topic)
	q.Set("per_page", strconv.Itoa(num))
	if e.email != "" {
		q.Set("mailto", e.email)
	}

	body, err := e.get(ctx, "/lists?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil // 404 / no body → empty, not an error
	}

	var parsed []struct {
		Name          string `json:"name"`
		URL           string `json:"url"`
		Description   string `json:"description"`
		ProjectsCount int    `json:"projects_count"`
		LastSyncedAt  string `json:"last_synced_at"`
		Repository    struct {
			FullName        string   `json:"full_name"`
			Archived        bool     `json:"archived"`
			StargazersCount int      `json:"stargazers_count"`
			Topics          []string `json:"topics"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("ecosystems: parse: %w", err)
	}

	out := make([]AwesomeListResult, 0, len(parsed))
	for _, l := range parsed {
		if params.MinStars > 0 && l.Repository.StargazersCount < params.MinStars {
			continue
		}
		if params.MinProjects > 0 && l.ProjectsCount < params.MinProjects {
			continue
		}
		if l.Repository.Archived {
			continue
		}
		out = append(out, AwesomeListResult{
			Name:          l.Name,
			FullName:      l.Repository.FullName,
			URL:           l.URL,
			Description:   l.Description,
			ProjectsCount: l.ProjectsCount,
			Stars:         l.Repository.StargazersCount,
			Topics:        l.Repository.Topics,
			LastSyncedAt:  l.LastSyncedAt,
			Archived:      l.Repository.Archived,
			Source:        "ecosystems",
		})
	}

	sortAwesomeLists(out, params.SortBy)
	if len(out) > num {
		out = out[:num]
	}
	return out, nil
}

// sortAwesomeLists orders results by the requested facet, descending. "stars"
// is the default (also applied for an unrecognized value) since stargazer
// count is the strongest available signal of a list's community trust.
func sortAwesomeLists(results []AwesomeListResult, sortBy string) {
	switch sortBy {
	case "projects":
		sort.SliceStable(results, func(i, j int) bool { return results[i].ProjectsCount > results[j].ProjectsCount })
	case "updated":
		sort.SliceStable(results, func(i, j int) bool { return results[i].LastSyncedAt > results[j].LastSyncedAt })
	default:
		sort.SliceStable(results, func(i, j int) bool { return results[i].Stars > results[j].Stars })
	}
}

func (e *EcosystemsAwesomeProvider) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", e.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey) // never logged
	}
	resp, err := e.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ecosystems: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("ecosystems: rate limited")
	}
	if resp.StatusCode == 404 {
		return nil, nil // not found → empty, not an error
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ecosystems: API error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

var _ AwesomeListProvider = (*EcosystemsAwesomeProvider)(nil)
