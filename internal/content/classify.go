package content

import (
	"net/url"
	"strings"
)

// SelfPromotionSignal detects when a source is a ranking list that places its
// own brand first. Attached to SourceClassification when detected.
type SelfPromotionSignal struct {
	Detected     bool   `json:"detected"`
	BrandDomain  string `json:"brandDomain"`  // e.g. "shopify.com"
	BrandToken   string `json:"brandToken"`   // e.g. "shopify"
	RankPosition int    `json:"rankPosition"` // 1-based position of brand in list
	Confidence   string `json:"confidence"`   // "high" | "medium" | "low"
}

// ConflictOfInterestSignal detects when an author has a financial stake in the
// citation's subject. Flags when a source's author wrote for or is affiliated
// with a company cited as credible in the citation text.
type ConflictOfInterestSignal struct {
	Detected          bool   `json:"detected"`
	AuthorAffiliation string `json:"authorAffiliation"` // e.g. "Shopify"
	ConflictType      string `json:"conflictType"`      // "employment" | "funded_by" | "owns_equity"
	CitedEntityName   string `json:"citedEntityName"`   // The entity mentioned in citation text
	Evidence          string `json:"evidence"`          // e.g. "Author byline shows 'at Shopify'"
	Confidence        string `json:"confidence"`        // "high" | "medium" | "low"
}

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
	// DomainReputation is the descriptive reliability tier for the host (#159),
	// from the transparent reputation dataset. nil/omitted for unlisted hosts
	// ("unknown") so the field never implies false confidence; descriptive only,
	// never used to gate or reorder results.
	DomainReputation *DomainReputation `json:"domainReputation,omitempty"`
	// SelfPromotion is non-nil when the page matches the ranking-list +
	// own-brand pattern (#244). Used to surface brand blogs that rank themselves first.
	SelfPromotion *SelfPromotionSignal `json:"selfPromotionSignal,omitempty"`
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

// Domain-category constants — the subject-area vocabulary (DomainCategory).
const (
	DomainCategoryAcademic  = "academic"
	DomainCategoryLegal     = "legal"
	DomainCategoryMedical   = "medical"
	DomainCategoryFinancial = "financial"
	DomainCategoryTechnical = "technical"
	DomainCategoryGeneral   = "general"
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
//   - body is the extracted page text used for self-promotion detection (#244);
//     pass "" to skip that check.
//
// Deterministic and allocation-light; safe to call per result.
func ClassifySource(rawURL string, authority float64, sig StructuredSignals, lens, body string) SourceClassification {
	host := classifyHost(rawURL)
	c := SourceClassification{
		SourceType:     classifySourceType(host, sig),
		AuthorityTier:  authorityTier(authority),
		DomainCategory: domainCategory(lens, host),
	}
	// Reputation (#159): attach only when the dataset knows the host — an
	// "unknown" tier carries no signal, so leave it nil to keep output clean and
	// avoid implying false confidence.
	if rep := LookupDomainReputation(host); rep.Tier != "" && rep.Tier != ReputationUnknown {
		c.DomainReputation = &rep
	}
	// Self-promotion signal (#244): detect if page ranks its own brand first
	if body != "" {
		c.SelfPromotion = DetectSelfPromotion(host, body)
	}
	return c
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
	case isLegalPrimaryHost(host):
		return SourceTypeGovernment
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
	for _, h := range []string{
		"arxiv.org", "biorxiv.org", "medrxiv.org", "nature.com", "science.org", "nih.gov",
		"ieee.org", "acm.org", "semanticscholar.org", "ssrn.com", "plos.org", "springer.com",
		"sciencedirect.com", "jstor.org", "doaj.org",
		// Major journal publishers — many serve article landing pages without the
		// Highwire citation_* meta on every tier, so the host signal is what lets
		// scrape_page's scholarly DOI detection (#199) engage on them.
		"thelancet.com", "cell.com", "bmj.com", "wiley.com", "tandfonline.com",
		"sagepub.com", "oup.com", "pnas.org", "mdpi.com", "frontiersin.org",
		"cambridge.org", "elsevier.com", "nejm.org", "jamanetwork.com", "ahajournals.org",
		"apa.org", "acs.org", "rsc.org", "iop.org", "aps.org",
	} {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// isLegalPrimaryHost returns true for hosts that serve primary legal records
// (court opinions, legislation, primary legal texts). Checked before the generic
// .edu catch-all so that legal-primary .edu hosts (e.g. law.cornell.edu) are
// classified government rather than peer_reviewed.
func isLegalPrimaryHost(host string) bool {
	for _, h := range []string{
		"courtlistener.com", // Free Law Project — US court opinions
		"justia.com",        // Justia — US case law and codes
		"law.cornell.edu",   // Cornell LII — primary legislation texts
	} {
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

// DetectConflictOfInterest checks whether an author bio or byline shows
// affiliation with a company/entity mentioned in the citation text. Returns nil
// when no conflict is detected (conservative — prioritize false negatives).
func DetectConflictOfInterest(authorBio, citationText string) *ConflictOfInterestSignal {
	if authorBio == "" || citationText == "" {
		return nil
	}

	bioLower := strings.ToLower(authorBio)

	// Extract company mentions from citation (patterns like "Company", "at Company", "Company's")
	companies := extractCompanyMentions(citationText)

	// Check if any company appears in the author bio with employment indicators
	employmentKeywords := []string{" at ", " works at ", ", ", " founder ", " ceo ", " engineer ", " manager "}
	fundedKeywords := []string{"funded by ", "grant from ", "supported by "}
	equityKeywords := []string{"advisor ", "board member ", "shareholder ", "investor "}

	for _, company := range companies {
		companyLower := strings.ToLower(company)
		if len(companyLower) < 3 {
			continue
		}

		// Check employment indicators
		for _, kw := range employmentKeywords {
			if strings.Contains(bioLower, kw+companyLower) || strings.Contains(bioLower, companyLower+kw) {
				return &ConflictOfInterestSignal{
					Detected:          true,
					AuthorAffiliation: company,
					ConflictType:      "employment",
					CitedEntityName:   company,
					Evidence:          "Author bio mentions employment at " + company,
					Confidence:        "high",
				}
			}
		}

		// Check funding/grant mentions
		for _, kw := range fundedKeywords {
			if strings.Contains(bioLower, kw+companyLower) {
				return &ConflictOfInterestSignal{
					Detected:          true,
					AuthorAffiliation: company,
					ConflictType:      "funded_by",
					CitedEntityName:   company,
					Evidence:          "Author bio mentions funding from " + company,
					Confidence:        "medium",
				}
			}
		}

		// Check equity/board mentions
		for _, kw := range equityKeywords {
			if strings.Contains(bioLower, kw+companyLower) {
				return &ConflictOfInterestSignal{
					Detected:          true,
					AuthorAffiliation: company,
					ConflictType:      "owns_equity",
					CitedEntityName:   company,
					Evidence:          "Author bio mentions equity stake in " + company,
					Confidence:        "medium",
				}
			}
		}
	}

	return nil
}

// extractCompanyMentions extracts company/entity names from text. Conservative
// heuristic: looks for capitalized words and common company patterns.
func extractCompanyMentions(text string) []string {
	var companies []string
	seen := make(map[string]bool)

	// Split on sentence boundaries and look for capitalized proper nouns
	sentences := strings.Split(text, ".")
	for _, sent := range sentences {
		words := strings.Fields(sent)
		for i, word := range words {
			// Look for capitalized words (simple proper noun detection)
			if len(word) > 2 && word[0] >= 'A' && word[0] <= 'Z' {
				// Skip common non-company words
				if !isCommonWord(word) && !seen[word] {
					companies = append(companies, strings.TrimRight(word, ",'\":;"))
					seen[word] = true
				}
			}
			// Look for known company patterns like "Inc.", "LLC", "Corp."
			if i < len(words)-1 {
				nextWord := words[i+1]
				if isCompanySuffix(nextWord) && len(word) > 2 && word[0] >= 'A' && word[0] <= 'Z' {
					company := word + " " + nextWord
					if !seen[company] {
						companies = append(companies, company)
						seen[company] = true
					}
				}
			}
		}
	}

	return companies
}

// isCommonWord filters out articles, prepositions, and other non-brand words
func isCommonWord(word string) bool {
	common := []string{"The", "A", "An", "Is", "Are", "Was", "Were", "To", "In", "On", "At", "For", "Of", "And", "Or", "By", "As", "The"}
	for _, c := range common {
		if strings.EqualFold(word, c) {
			return true
		}
	}
	return false
}

// isCompanySuffix checks if a word is a company type indicator
func isCompanySuffix(word string) bool {
	word = strings.ToLower(strings.TrimRight(word, ".,;:"))
	suffixes := []string{"inc", "llc", "corp", "ltd", "co", "gmbh", "ag", "sa", "bv", "nv"}
	for _, s := range suffixes {
		if word == s {
			return true
		}
	}
	return false
}

// DetectSelfPromotion checks whether body contains a ranking list that puts
// the page's own domain brand in position 1. Returns nil when the pattern is
// not detected (conservative — false negatives > false positives).
func DetectSelfPromotion(host, body string) *SelfPromotionSignal {
	if host == "" || body == "" {
		return nil
	}

	// Extract brand token from host: "shopify.com" → "shopify"
	parts := strings.Split(host, ".")
	if len(parts) == 0 {
		return nil
	}
	brandToken := strings.ToLower(parts[0])
	if len(brandToken) < 3 {
		return nil
	}

	// Look for ranking patterns: "1. BrandName" or "<li>1. BrandName" or "1. Brand — description"
	// Must be in first 2 items to count as self-promotion.
	bodyLower := strings.ToLower(body)

	// Pattern 1: markdown "1. " at line start. Strip any leading heading ("###")
	// or list ("-", "*") markers first — real listicles often render each entry
	// as a markdown heading ("### 1. Shopify"), so the bare-line check alone misses
	// them.
	lines := strings.Split(bodyLower, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimLeft(trimmed, "#*->•")
		trimmed = strings.TrimSpace(trimmed)
		if strings.HasPrefix(trimmed, "1.") || strings.HasPrefix(trimmed, "1 ") {
			// Extract next word
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				nextWord := strings.ToLower(parts[1])
				// Remove trailing punctuation
				nextWord = strings.TrimSuffix(strings.TrimSuffix(nextWord, ","), "—")
				if strings.Contains(nextWord, brandToken) || nextWord == brandToken {
					return &SelfPromotionSignal{
						Detected:     true,
						BrandDomain:  host,
						BrandToken:   brandToken,
						RankPosition: 1,
						Confidence:   "high",
					}
				}
			}
			// Found the "1." list item but the brand is not in it — not
			// self-promotion (a list that ranks someone else first).
			break
		}
	}

	// Pattern 2: HTML <li>1. or ordered list with brand name
	if strings.Contains(bodyLower, "<ol>") {
		// Simple check: "<li>" followed by "1." or "#1" and brand token within next 200 chars
		idx := strings.Index(bodyLower, "<li>")
		if idx >= 0 && idx+200 < len(bodyLower) {
			segment := bodyLower[idx : idx+200]
			if (strings.Contains(segment, "1.") || strings.Contains(segment, "#1")) && strings.Contains(segment, brandToken) {
				return &SelfPromotionSignal{
					Detected:     true,
					BrandDomain:  host,
					BrandToken:   brandToken,
					RankPosition: 1,
					Confidence:   "high",
				}
			}
		}
	}

	return nil
}
