package content

import (
	"math"
	"strings"
	"time"
)

type QualityScore struct {
	Overall        float64 `json:"overall"`
	Relevance      float64 `json:"relevance"`
	Freshness      float64 `json:"freshness"`
	Authority      float64 `json:"authority"`
	ContentQuality float64 `json:"contentQuality"`
}

type QualityInput struct {
	Content     string
	URL         string
	Title       string
	Query       string
	PublishedAt time.Time
}

func ScoreQuality(input QualityInput) QualityScore {
	relevance := scoreRelevance(input.Content, input.Title, input.Query)
	freshness := scoreFreshness(input.PublishedAt)
	authority := scoreAuthority(input.URL)
	contentQuality := scoreContent(input.Content)

	overall := relevance*0.35 + freshness*0.20 + authority*0.25 + contentQuality*0.20

	return QualityScore{
		Overall:        math.Round(overall*100) / 100,
		Relevance:      math.Round(relevance*100) / 100,
		Freshness:      math.Round(freshness*100) / 100,
		Authority:      math.Round(authority*100) / 100,
		ContentQuality: math.Round(contentQuality*100) / 100,
	}
}

// relevanceStopWords are common function words excluded from scoreRelevance
// keyword matching. Filtering them prevents high-frequency noise tokens (e.g.
// "the", "a", "go", "is") from diluting the IDF-like weights assigned to
// substantive query terms.
var relevanceStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"was": true, "were": true, "of": true, "in": true, "on": true,
	"at": true, "to": true, "for": true, "with": true, "by": true,
	"from": true, "and": true, "or": true, "not": true, "but": true,
	"go": true, "get": true, "set": true, "new": true, "use": true,
}

func scoreRelevance(content, title, query string) float64 {
	if query == "" {
		return 0.5
	}

	queryLower := strings.ToLower(query)
	contentLower := strings.ToLower(content)
	titleLower := strings.ToLower(title)

	allKeywords := strings.Fields(queryLower)
	if len(allKeywords) == 0 {
		return 0.5
	}

	// Filter stopwords; fall back to all keywords if the query is entirely stopwords.
	keywords := make([]string, 0, len(allKeywords))
	for _, kw := range allKeywords {
		if !relevanceStopWords[kw] {
			keywords = append(keywords, kw)
		}
	}
	if len(keywords) == 0 {
		keywords = allKeywords
	}

	// IDF-like weighting: longer tokens carry more information.
	// weight(token) = len(token) / avgLen(keywords), so a rare long term
	// like "neuroscience" outweighs a short one like "ai".
	var totalLen float64
	for _, kw := range keywords {
		totalLen += float64(len(kw))
	}
	avgLen := totalLen / float64(len(keywords))

	var titleScore, contentScore, totalWeight float64
	for _, kw := range keywords {
		w := float64(len(kw)) / avgLen
		totalWeight += w
		if strings.Contains(titleLower, kw) {
			titleScore += w
		}
		if strings.Contains(contentLower, kw) {
			contentScore += w
		}
	}

	titleScore /= totalWeight
	contentScore /= totalWeight

	return math.Min(1.0, titleScore*0.4+contentScore*0.6)
}

func scoreFreshness(publishedAt time.Time) float64 {
	if publishedAt.IsZero() {
		return 0.5
	}

	age := time.Since(publishedAt)
	switch {
	case age < 24*time.Hour:
		return 1.0
	case age < 7*24*time.Hour:
		return 0.9
	case age < 30*24*time.Hour:
		return 0.7
	case age < 365*24*time.Hour:
		return 0.5
	default:
		return 0.3
	}
}

func scoreAuthority(url string) float64 {
	urlLower := strings.ToLower(url)

	highAuthority := []string{
		".gov", ".edu", "nature.com", "science.org", "nih.gov",
		"who.int", "ieee.org", "acm.org", "arxiv.org", "wikipedia.org",
		"github.com", "stackoverflow.com", "developer.mozilla.org",
	}

	medAuthority := []string{
		"medium.com", "dev.to", "hackernews", "reddit.com",
		"bbc.com", "reuters.com", "nytimes.com",
	}

	for _, domain := range highAuthority {
		if strings.Contains(urlLower, domain) {
			return 0.9
		}
	}
	for _, domain := range medAuthority {
		if strings.Contains(urlLower, domain) {
			return 0.7
		}
	}

	// Fallback: consult domain_reputation.json so the authority score stays
	// consistent with the reputation tier for hosts not in the hardcoded lists
	// (e.g. courtlistener.com is tier:high but not .gov/.edu).
	if host := classifyHost(url); host != "" {
		rep := LookupDomainReputation(host)
		switch rep.Tier {
		case ReputationHigh:
			return 0.9
		case ReputationMixed:
			return 0.7
		}
	}

	return 0.5
}

func scoreContent(content string) float64 {
	if content == "" {
		return 0
	}

	length := len(content)
	words := len(strings.Fields(content))
	paragraphs := strings.Count(content, "\n\n") + 1

	var score float64

	switch {
	case words > 500:
		score += 0.3
	case words > 200:
		score += 0.2
	case words > 50:
		score += 0.1
	}

	if paragraphs > 3 {
		score += 0.2
	}

	avgWordLen := float64(length) / math.Max(float64(words), 1)
	if avgWordLen > 3 && avgWordLen < 10 {
		score += 0.2
	}

	if strings.Contains(content, "http") {
		score += 0.1
	}

	sentences := strings.Count(content, ".") + strings.Count(content, "!") + strings.Count(content, "?")
	if sentences > 5 {
		score += 0.2
	}

	return math.Min(1.0, score)
}
