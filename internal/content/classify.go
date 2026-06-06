package content

import (
	"net/url"
	"strings"
)

// SourceClassification is the typed, categorical companion to the numeric
// QualityScore (#62). Where ScoreQuality yields floats an LLM can't reliably
// turn into natural-language hedging, these labels let it say "according to a
// peer-reviewed study…" vs "a blog post…". All fields are best-effort and
// degrade to a safe default ("unknown"/"general") rather than guessing.
type SourceClassification struct {
	// SourceType is the kind of source: peer_reviewed, official_docs, government,
	// news_publication, blog, forum, wiki, social_media, or unknown.
	SourceType string `json:"sourceType"`
	// AuthorityTier bands the numeric Authority score: high | medium | low.
	AuthorityTier string `json:"authorityTier"`
	// DomainCategory is the subject area: academic, legal, medical, financial,
	// technical, or general.
	DomainCategory string `json:"domainCategory"`
}

// Source-type constants — the closed vocabulary callers can switch on.
const (
	SourceTypePeerReviewed = "peer_reviewed"
	SourceTypeOfficialDocs = "official_docs"
	SourceTypeGovernment   = "government"
	SourceTypeNews         = "news_publication"
	SourceTypeBlog         = "blog"
	SourceTypeForum        = "forum"
	SourceTypeWiki         = "wiki"
	SourceTypeSocial       = "social_media"
	SourceTypeUnknown      = "unknown"
)

// StructuredSignals is the decoupled view of scraped structured data that
// source-type classification needs. The scraper package (which owns the rich
// StructuredData type) builds this from it, so content has no dependency on
// scraper (avoiding an import cycle).
type StructuredSignals struct {
	// SchemaTypes are the top-level Schema.org @type values from JSON-LD blocks
	// (any case; the classifier lowercases). Empty when none were extracted.
	SchemaTypes []string
	// HasCitationMeta is true when Highwire citation_* meta was present — a strong
	// peer-reviewed signal.
	HasCitationMeta bool
}

// ClassifySource derives the typed classification for a source.
//
//   - authority is the numeric QualityScore.Authority (0–1), banded into a tier.
//   - sig holds the decoupled structured-data signals (zero value if none) — the
//     authoritative source_type signal when present.
//   - lens is the active search lens (may be "") — the primary domain_category
//     signal, with a URL-host heuristic as fallback.
//
// Deterministic and allocation-light; safe to call per result.
func ClassifySource(rawURL string, authority float64, sig StructuredSignals, lens string) SourceClassification {
	host := classifyHost(rawURL)
	return SourceClassification{
		SourceType:     classifySourceType(host, sig),
		AuthorityTier:  authorityTier(authority),
		DomainCategory: domainCategory(lens, host),
	}
}

// authorityTier bands the numeric authority score. Thresholds mirror the
// scoreAuthority bands (0.9 high-authority hosts, 0.7 medium, 0.5 default).
func authorityTier(authority float64) string {
	switch {
	case authority >= 0.8:
		return "high"
	case authority >= 0.5:
		return "medium"
	default:
		return "low"
	}
}

// classifySourceType resolves the source type, best signal first:
//  1. Schema.org JSON-LD @type / Highwire citation_* meta (authoritative).
//  2. Domain/URL host heuristic.
//  3. "unknown" when indeterminate — never a confident guess.
func classifySourceType(host string, sig StructuredSignals) string {
	if t := sourceTypeFromStructured(sig); t != "" {
		return t
	}
	if t := sourceTypeFromHost(host); t != "" {
		return t
	}
	return SourceTypeUnknown
}

// sourceTypeFromStructured reads the authoritative typed signal: Highwire
// citation_* meta is a strong peer-reviewed marker, and a JSON-LD @type maps to
// a source type. Returns "" if no decisive signal is present.
func sourceTypeFromStructured(sig StructuredSignals) string {
	if sig.HasCitationMeta {
		return SourceTypePeerReviewed
	}
	for _, st := range sig.SchemaTypes {
		if t := schemaTypeToSourceType(strings.ToLower(strings.TrimSpace(st))); t != "" {
			return t
		}
	}
	return ""
}

// schemaTypeToSourceType maps a Schema.org @type to our source-type vocabulary.
func schemaTypeToSourceType(schemaType string) string {
	switch schemaType {
	case "scholarlyarticle", "medicalscholarlyarticle":
		return SourceTypePeerReviewed
	case "newsarticle", "reportagenewsarticle", "analysisnewsarticle":
		return SourceTypeNews
	case "governmentservice", "gov(entity)", "governmentorganization":
		return SourceTypeGovernment
	case "blogposting", "blog":
		return SourceTypeBlog
	case "techarticle", "apireference":
		return SourceTypeOfficialDocs
	case "discussionforumposting", "qapage":
		return SourceTypeForum
	case "socialmediaposting":
		return SourceTypeSocial
	default:
		return ""
	}
}

// sourceTypeFromHost is the heuristic fallback when no structured data decides
// the type. Conservative: only well-known patterns, else "".
func sourceTypeFromHost(host string) string {
	switch {
	case host == "":
		return ""
	case strings.HasSuffix(host, ".gov") || strings.Contains(host, ".gov."):
		return SourceTypeGovernment
	case host == "wikipedia.org" || strings.HasSuffix(host, ".wikipedia.org") || host == "wikidata.org":
		return SourceTypeWiki
	case isAcademicHost(host):
		return SourceTypePeerReviewed
	case isOfficialDocsHost(host):
		return SourceTypeOfficialDocs
	case isNewsHost(host):
		return SourceTypeNews
	case isForumHost(host):
		return SourceTypeForum
	case isSocialHost(host):
		return SourceTypeSocial
	case isBlogHost(host):
		return SourceTypeBlog
	default:
		return ""
	}
}

func isAcademicHost(host string) bool {
	if strings.HasSuffix(host, ".edu") || strings.Contains(host, ".edu.") || strings.HasSuffix(host, ".ac.uk") {
		return true
	}
	for _, h := range []string{"arxiv.org", "biorxiv.org", "medrxiv.org", "nature.com", "science.org", "nih.gov", "ieee.org", "acm.org", "semanticscholar.org", "ssrn.com", "plos.org", "springer.com", "sciencedirect.com", "jstor.org", "doaj.org"} {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func isOfficialDocsHost(host string) bool {
	for _, h := range []string{"developer.mozilla.org", "docs.python.org", "pkg.go.dev", "kubernetes.io", "docs.docker.com", "learn.microsoft.com", "developer.apple.com", "developer.android.com", "docs.aws.amazon.com", "cloud.google.com", "readthedocs.io"} {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return strings.HasPrefix(host, "docs.")
}

func isNewsHost(host string) bool {
	for _, h := range []string{"bbc.com", "bbc.co.uk", "reuters.com", "nytimes.com", "washingtonpost.com", "theguardian.com", "wsj.com", "apnews.com", "bloomberg.com", "cnn.com", "ft.com", "economist.com"} {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func isForumHost(host string) bool {
	for _, h := range []string{"stackoverflow.com", "stackexchange.com", "reddit.com", "news.ycombinator.com", "quora.com", "discourse.org"} {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func isSocialHost(host string) bool {
	for _, h := range []string{"twitter.com", "x.com", "facebook.com", "instagram.com", "linkedin.com", "tiktok.com", "threads.net", "mastodon.social", "bsky.app"} {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func isBlogHost(host string) bool {
	for _, h := range []string{"medium.com", "dev.to", "substack.com", "wordpress.com", "blogspot.com", "hashnode.dev", "ghost.io"} {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return strings.HasPrefix(host, "blog.")
}

// domainCategory resolves the subject area. The active lens is the strongest
// signal (the caller explicitly scoped the search); a host heuristic is the
// fallback; "general" when neither decides.
func domainCategory(lens, host string) string {
	if c := lensToCategory(lens); c != "" {
		return c
	}
	if c := hostToCategory(host); c != "" {
		return c
	}
	return "general"
}

// lensToCategory maps a search lens to a domain category. Lenses not in the map
// (e.g. "news", "docs") fall through to "" so the host heuristic / "general"
// applies.
func lensToCategory(lens string) string {
	switch lens {
	case "academic", "science", "academic-extended":
		return "academic"
	case "legal":
		return "legal"
	case "medical", "clinical":
		return "medical"
	case "finance":
		return "financial"
	case "programming", "tech", "security", "devops":
		return "technical"
	default:
		return ""
	}
}

func hostToCategory(host string) string {
	switch {
	case host == "":
		return ""
	case isAcademicHost(host):
		return "academic"
	case isOfficialDocsHost(host) || isForumHost(host):
		return "technical"
	default:
		return ""
	}
}

// classifyHost returns the registrable host of a URL (lowercased, no port, no
// leading "www."), or "" if unparseable. Shared shape with coverage.hostOf but
// kept local so classification has no cross-file coupling.
func classifyHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}
