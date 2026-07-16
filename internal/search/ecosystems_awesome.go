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
//
// Topic matching is exact-string and case-sensitive with no built-in fuzzy
// matching (verified live: "Mental Health" and "mental health" both 404;
// "mental-health" returns 5 results) and the taxonomy itself is thin outside
// technical domains (verified live: "personal-finance" and "parenting" have
// no matching slug at all, though "finance" and "parent" do). doSearch
// compensates with two client-side layers: (1) lowercase + hyphenate the
// input before every call, and (2) on a compound miss, retry each
// substantive word independently and merge — see splitTopicWords /
// fetchWordFallback. An "awesome-<topic>" prefix retry was tried and
// dropped: topic slugs are GitHub topic tags, not repo-name conventions, so
// it never recovers anything a plain miss didn't already have.
type EcosystemsAwesomeProvider struct {
	apiKey        string
	email         string
	githubToken   string
	baseURL       string
	githubBaseURL string
	deps          Deps
}

// NewEcosystemsAwesomeProvider creates the provider. apiKey and email may
// both be "" (keyless — works at the shared "common" rate limit). githubToken
// is optional and only used by the GitHub topic-search fallback tier (#394);
// "" means that fallback runs unauthenticated against GitHub's public Search
// API rate limit.
func NewEcosystemsAwesomeProvider(apiKey, email, githubToken string, deps Deps) *EcosystemsAwesomeProvider {
	return &EcosystemsAwesomeProvider{
		apiKey:        apiKey,
		email:         email,
		githubToken:   githubToken,
		baseURL:       "https://awesome.ecosyste.ms/api/v1",
		githubBaseURL: githubAPIBaseURL,
		deps:          deps,
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

// SetBaseURL overrides the ecosyste.ms API base URL (testing).
func (e *EcosystemsAwesomeProvider) SetBaseURL(base string) { e.baseURL = base }

// SetGitHubBaseURL overrides the GitHub API base URL used by the tier-3
// fallback (testing).
func (e *EcosystemsAwesomeProvider) SetGitHubBaseURL(base string) { e.githubBaseURL = base }

func (e *EcosystemsAwesomeProvider) AwesomeLists(ctx context.Context, params AwesomeListSearchParams) ([]AwesomeListResult, error) {
	var results []AwesomeListResult
	err := e.deps.Breaker.Execute(func() error {
		var er error
		results, er = e.doSearch(ctx, params)
		return er
	})
	return results, err
}

// ecosystemsListItem mirrors one element of the /lists response.
type ecosystemsListItem struct {
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

// ecosystemsStopwords are filler words skipped when falling back to
// individual-word retries — querying them wastes an API call on a term with
// no topical signal (verified: "personal" alone returns unrelated low-star
// noise; see splitTopicWords fallback in doSearch).
var ecosystemsStopwords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "in": true, "on": true,
	"and": true, "or": true, "for": true, "to": true, "with": true,
	"is": true, "at": true, "by": true, "about": true,
}

// splitTopicWords lowercases and splits a topic/query into words on spaces,
// hyphens, and underscores, so "Mental Health", "mental-health", and
// "mental_health" all normalize to the same lookup and word set.
func splitTopicWords(topic string) []string {
	return strings.FieldsFunc(strings.ToLower(topic), func(r rune) bool {
		return r == ' ' || r == '-' || r == '_'
	})
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

	words := splitTopicWords(topic)
	normalized := strings.Join(words, "-")

	parsed, err := e.fetchRaw(ctx, normalized, num)
	if err != nil {
		return nil, err
	}

	// Multi-word input that missed as a compound slug (e.g. "personal-finance"
	// resolves to zero, but the constituent GitHub topics "personal" and
	// "finance" each exist independently) gets one retry per substantive word,
	// merged and deduped. A single-word miss (e.g. "parenting" — the real slug
	// is "parent", an unrecoverable stemming gap) is left as a genuine
	// no-match; there's nothing left to split. Deliberately NOT retried: an
	// "awesome-<topic>" prefix — verified via curl that ecosyste.ms topic
	// slugs are GitHub topic tags, not repo-name conventions, so
	// "awesome-parenting"/"awesome-personal-finance" both 404 the same way.
	if len(parsed) == 0 && len(words) > 1 {
		parsed = e.fetchWordFallback(ctx, words, num)
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

	// Tier 3: ecosyste.ms's topic taxonomy has real gaps (see doc comment
	// above) that fetchWordFallback cannot close on a single-word miss (e.g.
	// "parenting" — no constituent word to split). GitHub's own Search API
	// indexes the same "topic:awesome" tagging convention independently and
	// recovers exactly these misses (verified live 2026-07-15, issue #394).
	// Only tried when both prior tiers are still empty, and failures are
	// swallowed (best-effort recovery on top of an already-empty result, same
	// contract as fetchWordFallback).
	if len(out) == 0 {
		out = e.fetchGitHubTopicFallback(ctx, topic, params, num)
	}

	sortAwesomeLists(out, params.SortBy)
	if len(out) > num {
		out = out[:num]
	}
	return out, nil
}

// githubSearchRepoItem mirrors one element of GitHub's
// /search/repositories response items array — only the fields this fallback
// tier needs.
type githubSearchRepoItem struct {
	FullName    string   `json:"full_name"`
	HTMLURL     string   `json:"html_url"`
	Description string   `json:"description"`
	Stars       int      `json:"stargazers_count"`
	Topics      []string `json:"topics"`
	Archived    bool     `json:"archived"`
	PushedAt    string   `json:"pushed_at"`
}

type githubSearchRepoResponse struct {
	TotalCount int                    `json:"total_count"`
	Items      []githubSearchRepoItem `json:"items"`
}

// fetchGitHubTopicFallback queries GitHub's Search API for repositories
// tagged both "awesome" and the requested topic — a second, independent
// crawl of the same GitHub topic taxonomy ecosyste.ms indexes, filling the
// gaps its own taxonomy has no slug for at all (see issue #394). Applies the
// same MinStars/archived filters as the primary tier so callers see a
// consistent contract regardless of which tier produced the result. Returns
// nil (not an error) on any failure — this is a best-effort tier on top of
// an already-empty primary result, matching fetchWordFallback's contract.
func (e *EcosystemsAwesomeProvider) fetchGitHubTopicFallback(ctx context.Context, topic string, params AwesomeListSearchParams, num int) []AwesomeListResult {
	if topic == "" {
		return nil
	}
	q := url.Values{}
	query := "topic:awesome topic:" + topic
	if params.MinStars > 0 {
		query += " stars:>" + strconv.Itoa(params.MinStars-1)
	}
	query += " archived:false"
	q.Set("q", query)
	q.Set("per_page", strconv.Itoa(num))
	switch params.SortBy {
	case "updated":
		q.Set("sort", "updated")
	default:
		q.Set("sort", "stars")
	}
	q.Set("order", "desc")

	body, err := githubAPIRequest(ctx, e.deps.HTTPClient, e.githubBaseURL, "/search/repositories?"+q.Encode(), e.githubToken)
	if err != nil || len(body) == 0 {
		return nil
	}

	var parsed githubSearchRepoResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}

	out := make([]AwesomeListResult, 0, len(parsed.Items))
	for _, it := range parsed.Items {
		if it.Archived {
			continue
		}
		if params.MinProjects > 0 {
			// GitHub's Search API carries no equivalent to ecosyste.ms's
			// projects_count (curated-entry count) — this filter cannot be
			// evaluated against a GitHub-sourced result, so a caller that
			// requires it gets no GitHub-sourced results rather than an
			// unfiltered, potentially-misleading one.
			continue
		}
		out = append(out, AwesomeListResult{
			Name:         it.FullName,
			FullName:     it.FullName,
			URL:          it.HTMLURL,
			Description:  it.Description,
			Stars:        it.Stars,
			Topics:       it.Topics,
			LastSyncedAt: it.PushedAt,
			Archived:     it.Archived,
			Source:       "github",
		})
	}
	return out
}

// fetchRaw issues one /lists?topic= call and unmarshals the raw items.
func (e *EcosystemsAwesomeProvider) fetchRaw(ctx context.Context, topic string, num int) ([]ecosystemsListItem, error) {
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

	var parsed []ecosystemsListItem
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("ecosystems: parse: %w", err)
	}
	return parsed, nil
}

// fetchWordFallback retries a compound topic that missed as one slug (e.g.
// "personal-finance") by querying each substantive word independently (e.g.
// "personal", "finance") and merging the results, deduped by repository full
// name. Per-word failures (network error, rate limit) are skipped rather
// than propagated — this is a best-effort recovery on top of an already-
// empty result, not the primary path. Stopwords are skipped since they carry
// no topical signal and only cost an API call (verified: "personal" alone
// returns unrelated low-star noise that the star-sort at the end of doSearch
// naturally buries below any real topical match anyway).
func (e *EcosystemsAwesomeProvider) fetchWordFallback(ctx context.Context, words []string, num int) []ecosystemsListItem {
	seen := make(map[string]bool)
	var merged []ecosystemsListItem
	for _, w := range words {
		if len(w) < 2 || ecosystemsStopwords[w] {
			continue
		}
		items, err := e.fetchRaw(ctx, w, num)
		if err != nil {
			continue
		}
		for _, it := range items {
			key := it.Repository.FullName
			if key == "" {
				key = it.URL
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, it)
		}
	}
	return merged
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

// userAgent mirrors the crossref.go/retraction.go convention: embed the
// contact email in parens when configured, matching ecosyste.ms's own
// documented "polite" example (User-Agent: MyApp/1.0 (contact: you@example.com)).
func (e *EcosystemsAwesomeProvider) userAgent() string {
	if e.email == "" {
		return "web-researcher-mcp/1.0"
	}
	return "web-researcher-mcp/1.0 (mailto:" + e.email + ")"
}

func (e *EcosystemsAwesomeProvider) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", e.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	// Two independent gates in front of ecosyste.ms, both confirmed by direct
	// curl A/B testing (varying only the header under test):
	//  1. A literal-string block on Go's default "Go-http-client/*" User-Agent
	//     — 429s even at the "polite" tier with >99% of quota remaining. Any
	//     other UA string, descriptive or not, passes this gate.
	//  2. The APISIX conditional-rate-limit.lua plugin, which classifies a
	//     request "polite" (15,000/period) vs. "anonymous" (5,000/period)
	//     based on an email-shaped string in the mailto param OR the
	//     User-Agent header. mailto is already sent as a query param
	//     (doSearch) and alone is sufficient for tier classification — the
	//     email mirrored into the UA here is a second, redundant signal for
	//     gate 2, matching ecosyste.ms's own documented example format, not a
	//     workaround for gate 1.
	req.Header.Set("User-Agent", e.userAgent())
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
