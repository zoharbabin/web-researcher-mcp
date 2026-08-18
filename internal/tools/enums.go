package tools

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// mustSchemaFor infers a *jsonschema.Schema from T's struct tags (the same
// inference mcp.AddTool performs internally) so a registerX function can
// start from it and override specific Properties[...].Enum entries before
// passing the result as mcp.Tool.InputSchema. Panics on error — this only
// runs once per tool at server startup from a struct literal's own field
// tags, so a failure here is a programmer error (a malformed jsonschema tag),
// not a runtime/input condition; mirrors the go-sdk's own toolschemas example
// (examples/server/toolschemas/main.go), which uses log.Fatal for the same
// reason.
func mustSchemaFor[T any]() *jsonschema.Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("tools: failed to infer schema for %T: %v", *new(T), err))
	}
	return s
}

// This file is the single source of truth for every JSON-Schema `enum` value
// applied to a tool's InputSchema (issue #548). Each helper returns []any
// (jsonschema.Schema.Enum's element type) so callers can assign it directly:
//
//	customSchema.Properties["provider"].Enum = webProviderEnum()
//
// Provider-family helpers are sourced from the search package's
// Supported*Providers slices — never a hand-duplicated literal — so an enum
// can never drift from the resolver that actually accepts it. Where a tool's
// resolver honors a WIDER set than its own prose historically documented
// (e.g. search_and_scrape's resolveProvider() accepts the full web list, not
// just the 11 providers its description named), the enum reflects the
// resolver's real accepted set, not the narrower legacy prose — rejecting a
// value the runtime would otherwise accept is worse than accepting one a
// tool's own docs under-advertised.
//
// Non-provider helpers cover every other closed-vocabulary field discovered
// during the #548 inventory sweep. A field stays prose-only (no helper here)
// when its vocabulary is genuinely open-ended (e.g. legal_search's
// jurisdiction, filing_search's form_type, econ_search's units) — those are
// deliberately excluded; see the tool's own file for the reasoning.
//
// toAny converts a []string to []any, the type jsonschema.Schema.Enum and
// jsonschema.Schema.Items.Enum both require.
func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// webProviderEnum is the full web-search provider list, used by every tool
// that resolves its provider via the generic resolveProvider() (web_search,
// search_and_scrape, image_search, news_search, monitor_query_save,
// monitor_query_check) — regardless of each tool's own narrower historical
// prose, since resolveProvider() accepts any name in search.SupportedProviders.
func webProviderEnum() []any { return toAny(search.SupportedProviders) }

// academicProviderEnum unions the auto-routed academic providers, the
// explicit-only academic providers (reachable only via an explicit request,
// e.g. scholarapi), and the web-search fallback list — mirroring
// resolveAcademicSearcher's full accepted set.
func academicProviderEnum() []any {
	seen := make(map[string]bool)
	var all []string
	for _, lists := range [][]string{
		search.SupportedAcademicProviders,
		search.AcademicProvidersExplicitOnly,
		search.SupportedProviders,
	} {
		for _, name := range lists {
			if !seen[name] {
				seen[name] = true
				all = append(all, name)
			}
		}
	}
	return toAny(all)
}

// citationProviderEnum lists citation_graph's two supported providers.
func citationProviderEnum() []any { return []any{"semanticscholar", "openalex"} }

// patentProviderEnum unions the patent-specific providers with the web-search
// fallback list, mirroring resolvePatentSearcher's full accepted set.
func patentProviderEnum() []any {
	seen := make(map[string]bool)
	var all []string
	for _, lists := range [][]string{
		search.SupportedPatentProviders,
		search.SupportedProviders,
	} {
		for _, name := range lists {
			if !seen[name] {
				seen[name] = true
				all = append(all, name)
			}
		}
	}
	return toAny(all)
}

func filingProviderEnum() []any  { return toAny(search.SupportedFilingProviders) }
func caseProviderEnum() []any    { return toAny(search.SupportedCaseProviders) }
func econProviderEnum() []any    { return toAny(search.SupportedEconProviders) }
func trialProviderEnum() []any   { return toAny(search.SupportedTrialProviders) }
func localProviderEnum() []any   { return toAny(search.SupportedLocalProviders) }
func monarchProviderEnum() []any { return toAny(search.SupportedMonarchProviders) }
func awesomeListProviderEnum() []any {
	return toAny(search.SupportedAwesomeListProviders)
}

// --- Non-provider enums -----------------------------------------------------

// webTimeRangeEnum covers web_search's time_range (4 values).
func webTimeRangeEnum() []any { return []any{"day", "week", "month", "year"} }

// newsTimeRangeEnum covers news_search's time_range (5 values — adds "hour").
// Distinct from webTimeRangeEnum; do not share.
func newsTimeRangeEnum() []any { return []any{"hour", "day", "week", "month", "year"} }

// webSafeEnum covers web_search and image_search's safe field.
func webSafeEnum() []any { return []any{"off", "medium", "high"} }

// newsSafeEnum covers news_search's safe field — a genuinely different value
// set from webSafeEnum ("moderate"/"strict" vs "medium"/"high"); do not share.
func newsSafeEnum() []any { return []any{"off", "moderate", "strict"} }

// imageSizeEnum covers image_search's size field.
func imageSizeEnum() []any {
	return []any{"huge", "icon", "large", "medium", "small", "xlarge", "xxlarge"}
}

// imageTypeEnum covers image_search's type field.
func imageTypeEnum() []any {
	return []any{"clipart", "face", "lineart", "stock", "photo", "animated"}
}

// imageColorTypeEnum covers image_search's color_type field.
func imageColorTypeEnum() []any { return []any{"color", "gray", "mono", "trans"} }

// imageDominantColorEnum covers image_search's dominant_color field.
func imageDominantColorEnum() []any {
	return []any{"black", "blue", "brown", "gray", "green", "orange", "pink", "purple", "red", "teal", "white", "yellow"}
}

// imageFileTypeEnum covers image_search's file_type field.
func imageFileTypeEnum() []any { return []any{"jpg", "gif", "png", "bmp", "svg", "webp"} }

// academicSourceEnum covers academic_search's source field.
func academicSourceEnum() []any {
	return []any{"all", "arxiv", "pubmed", "ieee", "nature", "springer"}
}

// sortByRelevanceDateEnum covers academic_search's and news_search's shared
// sort_by vocabulary (relevance/date).
func sortByRelevanceDateEnum() []any { return []any{"relevance", "date"} }

// awesomeSortByEnum covers awesome_list_search's sort_by field — a distinct
// vocabulary from sortByRelevanceDateEnum; do not share.
func awesomeSortByEnum() []any { return []any{"stars", "projects", "updated"} }

// patentSearchTypeEnum covers patent_search's search_type field.
func patentSearchTypeEnum() []any { return []any{"prior_art", "specific", "landscape"} }

// patentOfficeEnum covers patent_search's patent_office field, sourced from
// the officePrefixes map matchesPatentOffice validates against (patent.go).
func patentOfficeEnum() []any {
	return []any{"all", "US", "EP", "WO", "JP", "CN", "KR"}
}

// citationDirectionEnum covers citation_graph's direction field.
func citationDirectionEnum() []any { return []any{"cited_by", "references", "both"} }

// monarchOperationEnum covers monarch_search's operation field, the
// discriminator validated in buildMonarchParams's switch.
func monarchOperationEnum() []any {
	return []any{"semsim", "entity", "associations", "compare", "annotate"}
}

// monarchGroupEnum covers monarch_search's group field, validated against
// search.ValidSemsimGroup.
func monarchGroupEnum() []any {
	return []any{"Human Genes", "Mouse Genes", "Rat Genes", "Zebrafish Genes", "C. Elegans Genes", "Human Diseases"}
}

// localUnitsEnum covers local_search's units field.
func localUnitsEnum() []any { return []any{"metric", "imperial"} }

// sequentialConfidenceEnum covers sequential_search's confidence field.
func sequentialConfidenceEnum() []any { return []any{"high", "medium", "low"} }

// sequentialResponseModeEnum covers sequential_search's response_mode field.
// Empty string is NOT itself a valid enum member — omitting the field (its
// zero value) triggers buildSequentialResponse's auto-select branch, which is
// what omitempty + a non-required field already represents; the enum only
// constrains an explicitly-supplied value.
func sequentialResponseModeEnum() []any { return []any{"full", "summary"} }

// sequentialDepthEnum covers sequential_search's depth field.
func sequentialDepthEnum() []any { return []any{"quick", "standard", "thorough"} }

// scrapeModeEnum covers scrape_page's mode field.
func scrapeModeEnum() []any { return []any{"full", "preview", "raw"} }

// bibStyleEnum covers format_bibliography's style field, sourced directly from
// content.SupportedBibStyles so the two can never drift apart.
func bibStyleEnum() []any { return toAny(content.SupportedBibStyles) }

// auditBibFormatEnum covers audit_bibliography's format field. Unlike
// format_bibliography's style, "auto" is a real accepted value here (it
// triggers content.ParseBibliography's format-sniffing), so it is included.
func auditBibFormatEnum() []any { return []any{"auto", "csl-json", "ris", "bibtex"} }

// brandDepthEnum covers brand_research's depth field. NOTE (behavior change):
// brand_research currently silently coerces an invalid depth to "standard"
// rather than rejecting it (see brand_research.go). Applying this enum makes
// an invalid depth a hard schema-validation rejection instead — a deliberate,
// minor tightening called out in the #548 PR, not a no-op.
func brandDepthEnum() []any { return []any{"quick", "standard", "full"} }

// researchExportFormatEnum covers research_export's format field.
func researchExportFormatEnum() []any { return []any{"markdown", "json"} }

// companyReconPhasesEnum covers company_recon's phases field (a []string —
// assign to customSchema.Properties["phases"].Items.Enum, not .Enum).
func companyReconPhasesEnum() []any { return toAny(companyReconDefaultPhases) }

// clinicalPhaseEnum covers clinical_search's phase field — ClinicalTrials.gov
// v2's closed phase vocabulary (verified against the live API, 2026-08-18;
// see clinicalPhaseCodes in clinicaltrials.go). Free-form variants ("Phase 3",
// "phase_3", "3") are still accepted at runtime via normalizePhase — the enum
// documents the canonical spelling, matching the registry's own Phases output.
func clinicalPhaseEnum() []any {
	return []any{"PHASE1", "PHASE2", "PHASE3", "PHASE4", "EARLY_PHASE1"}
}
