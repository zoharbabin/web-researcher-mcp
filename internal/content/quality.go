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

func scoreRelevance(content, title, query string) float64 {
	if query == "" {
		return 0.5
	}

	queryLower := strings.ToLower(query)
	contentLower := strings.ToLower(content)
	titleLower := strings.ToLower(title)

	keywords := strings.Fields(queryLower)
	if len(keywords) == 0 {
		return 0.5
	}

	var titleHits, contentHits int
	for _, kw := range keywords {
		if strings.Contains(titleLower, kw) {
			titleHits++
		}
		if strings.Contains(contentLower, kw) {
			contentHits++
		}
	}

	titleScore := float64(titleHits) / float64(len(keywords))
	contentScore := float64(contentHits) / float64(len(keywords))

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
