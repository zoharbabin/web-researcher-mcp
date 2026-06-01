package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
)

// ErrorKind classifies tool errors for programmatic handling by LLM clients.
type ErrorKind string

const (
	ErrKindRateLimit          ErrorKind = "rate_limited"
	ErrKindAuth               ErrorKind = "auth_required"
	ErrKindBlocked            ErrorKind = "blocked"
	ErrKindNetwork            ErrorKind = "network"
	ErrKindContentEmpty       ErrorKind = "content_empty"
	ErrKindBrowserUnavailable ErrorKind = "browser_unavailable"
	ErrKindValidation         ErrorKind = "validation"
	ErrKindUpstream           ErrorKind = "upstream_unavailable"
	ErrKindConfig             ErrorKind = "config"
	ErrKindSessionNotFound    ErrorKind = "session_not_found"
)

// SuggestedAction tells the LLM what recovery strategy to consider.
type SuggestedAction string

const (
	ActionRetryAfterDelay      SuggestedAction = "retry_after_delay"
	ActionTryDifferentProvider SuggestedAction = "try_different_provider"
	ActionCheckAPIKey          SuggestedAction = "check_api_key"
	ActionBroadenQuery         SuggestedAction = "broaden_query"
	ActionInformUser           SuggestedAction = "inform_user"
	ActionReportBug            SuggestedAction = "report_bug"
)

// ToolError is the structured error metadata embedded in error responses.
type ToolError struct {
	Kind              ErrorKind       `json:"kind"`
	Retryable         bool            `json:"retryable"`
	RetryAfterSeconds *int            `json:"retryAfterSeconds,omitempty"`
	SuggestedAction   SuggestedAction `json:"suggestedAction"`
	Provider          string          `json:"provider,omitempty"`
	Alternatives      []string        `json:"alternatives,omitempty"`
	Detail            string          `json:"detail,omitempty"`
	RecoveryHint      *RecoveryHint   `json:"recoveryHint,omitempty"`
}

// RecoveryHint carries machine-readable guidance for recovering from a
// session_not_found error so the client can decide to resume or restart
// without the server retaining the lost session's data. Emitted when a
// multi-pod HTTP deployment routes a follow-up step to a pod that does not
// hold the (in-memory) session.
type RecoveryHint struct {
	// LastKnownStep is the last step the caller believed it completed.
	LastKnownStep int `json:"lastKnownStep"`
	// CanResume is false when no session state survives (the client should
	// restart at step 1); true would indicate resumable state is available.
	CanResume bool `json:"canResume"`
}

// sessionNotFoundError builds the structured session_not_found result with a
// recovery hint derived from the typed session.SessionNotFoundError.
func sessionNotFoundError(lastKnownStep int) *mcp.CallToolResult {
	return structuredError(
		"This research session is not available on this instance. It may have expired (sessions last 4 hours), or in a multi-instance deployment your request reached a different server than the one holding it. Start a new session with stepNumber=1 (omit sessionId), or recover with get_research_session.",
		ToolError{
			Kind:            ErrKindSessionNotFound,
			Retryable:       false,
			SuggestedAction: ActionInformUser,
			RecoveryHint: &RecoveryHint{
				LastKnownStep: lastKnownStep,
				CanResume:     false,
			},
		},
	)
}

// structuredError returns an MCP error result with dual-format content:
// line 1 is a natural-language message for LLMs, followed by a JSON block
// with machine-readable error metadata for programmatic handling.
func structuredError(msg string, te ToolError) *mcp.CallToolResult {
	jsonBytes, _ := json.Marshal(map[string]any{"error": te})
	text := msg + "\n\n" + string(jsonBytes)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
		IsError: true,
	}
}

// mapScrapeErrorKind converts scraper.ErrorKind to the tool-level ErrorKind.
func mapScrapeErrorKind(k scraper.ErrorKind) ErrorKind {
	switch k {
	case scraper.ErrNetwork:
		return ErrKindNetwork
	case scraper.ErrBlocked:
		return ErrKindBlocked
	case scraper.ErrBrowser:
		return ErrKindBrowserUnavailable
	case scraper.ErrContent:
		return ErrKindContentEmpty
	case scraper.ErrAuth:
		return ErrKindAuth
	case scraper.ErrRateLimit:
		return ErrKindRateLimit
	case scraper.ErrValidation:
		return ErrKindValidation
	default:
		return ErrKindUpstream
	}
}

// scrapeErrorToToolError builds a structured ToolError from a ScrapeError.
func scrapeErrorToToolError(se *scraper.ScrapeError) ToolError {
	te := ToolError{
		Kind: mapScrapeErrorKind(se.Kind),
	}
	switch se.Kind {
	case scraper.ErrValidation:
		// Permanent client/security rejection (bad scheme, empty host, SSRF /
		// private-IP / blocked-hostname denial). Never retryable; the caller
		// must change the URL, not retry or file a bug.
		te.Retryable = false
		te.SuggestedAction = ActionInformUser
	case scraper.ErrRateLimit:
		te.Retryable = true
		seconds := 60
		te.RetryAfterSeconds = &seconds
		te.SuggestedAction = ActionRetryAfterDelay
	case scraper.ErrBlocked:
		te.Retryable = true
		te.SuggestedAction = ActionReportBug
	case scraper.ErrBrowser:
		te.Retryable = false
		te.SuggestedAction = ActionReportBug
	case scraper.ErrContent:
		te.Retryable = true
		te.SuggestedAction = ActionReportBug
	case scraper.ErrAuth:
		te.Retryable = false
		te.SuggestedAction = ActionInformUser
	case scraper.ErrNetwork:
		te.Retryable = true
		te.SuggestedAction = ActionRetryAfterDelay
		seconds := 5
		te.RetryAfterSeconds = &seconds
	}
	// Scrape messages can include the target URL with embedded credentials;
	// mask before the detail reaches an LLM-facing result.
	te.Detail = audit.MaskSecrets(se.Message)
	return te
}

// extractProviderName attempts to extract the provider name from an error string.
func extractProviderName(err error) string {
	s := err.Error()
	for _, prefix := range []string{"google:", "brave:", "serper:", "searxng:", "searchapi:", "lens:", "uspto:", "epo:", "openalex:", "crossref:"} {
		if strings.HasPrefix(s, prefix[:len(prefix)-1]) || strings.Contains(s, prefix) {
			return prefix[:len(prefix)-1]
		}
	}
	return ""
}

// FailureInfo is returned in partial-success compound tool results.
type FailureInfo struct {
	URL             string          `json:"url"`
	Kind            string          `json:"kind,omitempty"`
	Reason          string          `json:"reason"`
	Retryable       bool            `json:"retryable"`
	SuggestedAction SuggestedAction `json:"suggestedAction,omitempty"`
}

// failureFromScrapeError builds a FailureInfo from a scrape error.
func failureFromScrapeError(url string, err error) FailureInfo {
	f := FailureInfo{URL: url, Reason: audit.MaskSecrets(err.Error())}
	var se *scraper.ScrapeError
	if ok := isAsScrapeError(err, &se); ok {
		f.Kind = string(mapScrapeErrorKind(se.Kind))
		te := scrapeErrorToToolError(se)
		f.Retryable = te.Retryable
		f.SuggestedAction = te.SuggestedAction
	}
	return f
}

func isAsScrapeError(err error, target **scraper.ScrapeError) bool {
	if se, ok := err.(*scraper.ScrapeError); ok {
		*target = se
		return true
	}
	return false
}

// ZeroResultHints provides context when a search returns no results.
type ZeroResultHints struct {
	Reason             string            `json:"reason"`
	ProvidersAttempted []string          `json:"providersAttempted,omitempty"`
	FiltersApplied     map[string]string `json:"filtersApplied,omitempty"`
	SuggestedActions   []HintAction      `json:"suggestedActions,omitempty"`
}

// HintAction is a suggested recovery action for zero-result or failed queries.
type HintAction struct {
	Action    string `json:"action"`
	Detail    string `json:"detail,omitempty"`
	Parameter string `json:"parameter,omitempty"`
	Value     string `json:"value,omitempty"`
}

// buildZeroResultHints constructs hints explaining why a search returned nothing.
func buildZeroResultHints(provider string, params map[string]string, alternatives []string) *ZeroResultHints {
	hints := &ZeroResultHints{
		Reason: "no_match",
	}
	if provider != "" {
		hints.ProvidersAttempted = []string{provider}
	}

	if len(params) > 0 {
		hints.FiltersApplied = params
		hints.Reason = "filters_too_restrictive"
		for k := range params {
			hints.SuggestedActions = append(hints.SuggestedActions, HintAction{
				Action:    "remove_filter",
				Parameter: k,
				Detail:    fmt.Sprintf("Remove %s filter to broaden results", k),
			})
			if len(hints.SuggestedActions) >= 3 {
				break
			}
		}
	}

	if len(alternatives) > 0 && len(hints.SuggestedActions) < 3 {
		hints.SuggestedActions = append(hints.SuggestedActions, HintAction{
			Action: "try_different_provider",
			Value:  alternatives[0],
			Detail: "Try a different search provider",
		})
	}

	return hints
}

// cachedResultWithMeta returns a structured result with cache freshness metadata in _meta.
func cachedResultWithMeta(data []byte, meta *cache.EntryMeta) *mcp.CallToolResult {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
		StructuredContent: json.RawMessage(data),
	}
	if meta != nil {
		result.Meta = mcp.Meta{
			"cached":        true,
			"ageSeconds":    meta.AgeSeconds(),
			"maxAgeSeconds": meta.MaxAgeSeconds(),
			"freshness":     meta.Freshness(),
		}
	} else {
		result.Meta = mcp.Meta{"cached": true}
	}
	return result
}

// freshResult returns a structured result marked as freshly fetched.
func freshResult(data []byte, ttl time.Duration) *mcp.CallToolResult {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
		StructuredContent: json.RawMessage(data),
	}
	result.Meta = mcp.Meta{
		"cached":        false,
		"ageSeconds":    0,
		"maxAgeSeconds": int(ttl.Seconds()),
		"freshness":     "fresh",
	}
	return result
}
