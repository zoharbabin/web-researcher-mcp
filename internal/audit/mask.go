package audit

import "regexp"

// Secret-redaction patterns applied at the audit/tools boundary. These are
// best-effort defense-in-depth: provider errors and metadata occasionally echo
// back credentials embedded in URLs or upstream messages, and those must never
// reach an audit sink or an LLM-facing error. Patterns are ordered most- to
// least-specific so that a longer match wins before a broader one runs.
//
// Each pattern is compiled once at package init (MustCompile) — the patterns
// are constant literals, so a compile failure is a programmer error caught by
// tests, never a runtime condition in a request path.
var (
	// Google API keys: "AIza" followed by 35 URL-safe chars.
	reGoogleKey = regexp.MustCompile(`AIza[0-9A-Za-z\-_]{35}`)
	// OpenAI / Anthropic style keys: "sk-" followed by >=20 chars.
	reSKKey = regexp.MustCompile(`sk-[0-9A-Za-z\-_]{20,}`)
	// Brave Search API keys: "BSA" followed by >=20 chars.
	reBSAKey = regexp.MustCompile(`BSA[0-9A-Za-z\-_]{20,}`)
	// HTTP Authorization bearer tokens (case-insensitive scheme).
	reBearer = regexp.MustCompile(`(?i)\bBearer\s+[0-9A-Za-z\-._~+/]+=*`)
	// Sensitive query-string params: key=..., token=..., api_key=...,
	// apikey=..., secret=..., password=..., access_token=..., consumer_secret=...
	// Captures the param name so it can be preserved while the value is redacted.
	reQueryParam = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|consumer[_-]?secret|token|secret|password|passwd|pwd|key)=[^&\s"']+`)
	// Bare 64-character hex strings (e.g. CACHE_ENCRYPTION_KEY material).
	reHex64 = regexp.MustCompile(`\b[0-9a-fA-F]{64}\b`)
)

const redacted = "[REDACTED]"

// MaskSecrets returns s with credential-like substrings replaced by a fixed
// redaction marker. It is safe to call on arbitrary text (error messages,
// metadata values, URLs) and is idempotent on already-masked input. An empty
// input returns an empty string.
//
// Ordering matters: more specific token shapes (Google/SK/BSA/bearer) run
// before the generic query-param and 64-hex patterns so that a long secret is
// never partially matched by a broader rule.
func MaskSecrets(s string) string {
	if s == "" {
		return s
	}
	s = reGoogleKey.ReplaceAllString(s, redacted)
	s = reSKKey.ReplaceAllString(s, redacted)
	s = reBSAKey.ReplaceAllString(s, redacted)
	s = reBearer.ReplaceAllString(s, "Bearer "+redacted)
	// Preserve the param name; redact only the value so logs stay diagnosable.
	s = reQueryParam.ReplaceAllString(s, "${1}="+redacted)
	s = reHex64.ReplaceAllString(s, redacted)
	return s
}
