package config

import (
	"fmt"
	"log/slog"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	GoogleAPIKey           string
	GoogleCX               string
	Search                 SearchConfig
	Port                   int
	OAuth                  OAuthConfig
	AllowedOrigins         []string
	CORSStrict             bool
	HTTP                   HTTPConfig
	CacheDir               string
	CacheMaxMemoryMB       int
	CacheEncryptionKey     string
	CacheEncryptionKeyPrev string
	// Cookie vault (#241). Only meaningful when Features.CookieVault is true; Load
	// validates the key (64 hex, distinct from the cache keys) and the bounds.
	CookieVaultKey        string
	CookieVaultTTL        time.Duration // max lifetime of any stored cookie bundle
	MaxConcurrentCaptures int           // simultaneous headed login captures
	CookieCaptureTimeout  time.Duration // how long a site_login window may stay open
	// CookieVaultProfileTTL is the absolute lifetime of a saved browser profile
	// (COOKIE_VAULT_PROFILE_TTL, issue #775).
	CookieVaultProfileTTL time.Duration
	// CookieVaultProfileMaxTotalMB caps the combined size of all saved browser
	// profiles on this node (COOKIE_VAULT_PROFILE_MAX_TOTAL_MB, issue #775).
	CookieVaultProfileMaxTotalMB int
	// CookieCaptureInHTTP lets site_login run in HTTP mode, where the login
	// window opens on the server's own display (COOKIE_CAPTURE_IN_HTTP, #775).
	CookieCaptureInHTTP bool
	// CookieVaultStdioConsent is the operator's explicit opt-in to record the
	// session_capture consent for STDIO_USER_ID at startup (COOKIE_VAULT_STDIO_CONSENT).
	// Honored only with the vault enabled, in STDIO mode, with a valid STDIO_USER_ID.
	CookieVaultStdioConsent       bool
	CacheIsolation                string
	RedisURL                      string
	RateLimit                     RateLimitConfig
	AllowPrivateIPs               bool
	AllowedDomains                []string
	ChromePath                    string
	ChromeNoSandbox               bool // CHROME_NO_SANDBOX: run saved-login browsers without Chrome's sandbox (containers)
	JinaDisabled                  bool
	BrowserIdleTimeout            time.Duration
	MaxScrapeConcurrency          int
	MaxBrowserConcurrency         int
	MaxScrapeConcurrencyPerTenant int
	MaxHTMLBytes                  int
	MaxDocumentBytes              int
	SessionTTL                    time.Duration
	SessionDataDir                string
	SessionMaxSteps               int
	LogLevel                      slog.Level
	LogFormat                     string
	MetricsEnabled                bool
	MetricsMaxTenants             int
	AdminAPIKey                   string
	AdminAPIKeyPrev               string
	DataRegion                    string
	Features                      FeatureConfig
	Audit                         AuditConfig
	ResearchPanel                 ResearchPanelConfig
	Circuit                       CircuitConfig

	// StdioUserID names the single local user for STDIO transport, where there
	// is no OAuth identity (the launching app owns the process, so it IS one
	// trusted user). When set, the server injects tenant=default/user=<value>
	// into STDIO request context, making the per-user regulated features
	// (memory, analytics) reachable. Empty (default) keeps the "anonymous"
	// behavior. Ignored whenever PORT is set (HTTP mode), where identity comes
	// from OAuth if configured, or stays anonymous in the unauthenticated
	// fallback. Validated to a safe charset since it flows into
	// cache/session/consent/data-subject keys.
	StdioUserID string

	// Warnings holds non-fatal configuration notices (e.g. deprecated env vars)
	// surfaced at startup. Load populates it; main.go logs each at WARN level.
	Warnings []string
}

// HTTPConfig holds HTTP-transport hardening knobs. All fields are ignored when
// Port==0 (STDIO mode). Defaults are permissive so long research responses are
// never truncated; WriteTimeout in particular defaults to 0 (unlimited).
type HTTPConfig struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
	MaxRequestBody    int
	CSP               string
	ReferrerPolicy    string
	PermissionsPolicy string
}

// FeatureConfig holds opt-in/opt-out toggles for additive output features that
// are safe-by-construction (content-only, deterministic, no personal data).
// Regulated features (memory, analytics, workspaces) live elsewhere and are
// gated by their own flags plus consent.
type FeatureConfig struct {
	// SourceRecommendations surfaces advisory "related higher-quality sources"
	// derived from the existing transparent quality signals. Content-based and
	// non-profiling, so it defaults ON; the field is additive (omitted when
	// nothing qualifies) and never re-ranks results.
	SourceRecommendations bool
	// GenerativeUI emits additive, mcp-auto-formatted renderable components (cards,
	// tables) built deterministically from already-extracted data. Defaults
	// OFF; when off, output is byte-for-byte unchanged.
	GenerativeUI bool

	// Regulated features (all default OFF). Each processes per-user personal
	// data and is gated by recorded consent (#89) + data-subject rights (#85).
	// The consent subsystem activates iff at least one of these is enabled —
	// there is no standalone CONSENT_ENABLED knob to drift out of sync.
	Memory        bool // #88 opt-in long-term cross-session memory
	UserAnalytics bool // #92 opt-in per-user analytics
	Workspaces    bool // #96 opt-in shared research workspaces
	Monitoring    bool // #273 opt-in saved query-monitor persistence
	CookieVault   bool // #241 opt-in encrypted cookie vault for login-gated scraping

	// MemoryRetention bounds how long a saved memory lives before auto-expiry
	// (#88). 0 → the store's default (90 days). "Data doesn't exist after TTL"
	// stays the safety property unless the operator extends it.
	MemoryRetention time.Duration

	// WorkspaceTTL bounds how long shared-workspace data lives (#96).
	// 0 → the store's default (30 days).
	WorkspaceTTL time.Duration

	// MonitoringTTL bounds the maximum lifetime of a saved query monitor
	// (#273). 0 → the tool's default (30 days).
	MonitoringTTL time.Duration
}

// RegulatedEnabled reports whether any consent-gated feature is on, which is
// the sole trigger for activating the consent subsystem.
func (f FeatureConfig) RegulatedEnabled() bool {
	return f.Memory || f.UserAnalytics || f.Workspaces || f.Monitoring || f.CookieVault
}

type AuditConfig struct {
	Enabled            bool
	OutputPath         string
	BufferSize         int
	IncludeRequestBody bool
	MaxBytes           int
	RetentionDays      int
	// WebhookURL is the optional SIEM export target (#466 half 2):
	// AUDIT_WEBHOOK_URL. Empty disables webhook export entirely.
	WebhookURL string
}

type SearchConfig struct {
	Provider           string
	Routing            string
	GoogleAPIKey       string
	GoogleCX           string
	BraveAPIKey        string
	BraveExtraSnippets bool
	SerperAPIKey       string
	SearchAPIKey       string
	YouDotComAPIKey    string
	TavilyAPIKey       string
	ExaAPIKey          string
	XQuikAPIKey        string
	// ParallelEnabled (PARALLEL_ENABLED) is the operator opt-in for the
	// Parallel provider. It is keyless, so without an explicit toggle merely
	// upgrading the binary would make a brand-new egress destination
	// selectable; the provider is not constructed unless this is true.
	ParallelEnabled bool
	// ParallelAPIKey (PARALLEL_API_KEY) is optional and only read when
	// ParallelEnabled is true. It is sent as a Bearer token to raise the
	// anonymous rate limit; empty keeps the connection anonymous.
	ParallelAPIKey   string
	JinaAPIKey       string // optional; the Jina Reader scraper tier works keyless, a key raises its rate limit
	SearXNGURL       string
	SearXNGBasicAuth string            // raw "user:password" for a SearXNG behind HTTP Basic auth; "" => none (never logged)
	SearXNGHeaders   map[string]string // validated static request headers for SearXNG; nil/empty => none
	CustomLensesPath string
	ThinThreshold    int // SEARCH_THIN_THRESHOLD: retry same provider with simplified query when result count <= this; 0 disables

	// Patent-specific providers (optional, enables structured patent search)
	USPTOAPIKey       string
	EPOConsumerKey    string
	EPOConsumerSecret string
	LensAPIToken      string

	// Academic-specific providers (optional, enables structured scholarly search)
	OpenAlexEmail         string
	CrossRefEmail         string
	SemanticScholarAPIKey string // optional; Semantic Scholar works keyless at a lower shared rate
	UnpaywallEmail        string // open-access PDF resolution; falls back to OpenAlexEmail when unset
	PubMedAPIKey          string // optional; PubMed E-utilities work keyless (~3 req/s), a key raises it (~10 req/s)
	PubMedEmail           string // optional NCBI contact (tool/email params) — falls back to OpenAlexEmail
	COREAPIKey            string // optional; CORE.ac.uk works keyless at a lower shared rate, a key raises the limit
	ScholarAPIKey         string // ScholarAPI — paid academic search with full-text access (scholarapi.net); excluded from auto-routing, use provider=scholarapi explicitly

	// Structured-domain providers (optional, enable filing/case/economic search)
	EDGARContactEmail  string // SEC EDGAR requires a contact email for its required User-Agent
	CourtListenerToken string // optional; CourtListener works keyless at a lower rate
	FREDAPIKey         string // Federal Reserve Economic Data API key

	// Internet Archive Save Page Now (archive_source, #196) — optional S3-style
	// credentials. Keyless SPN works without them; keys raise rate/reliability.
	IAAccessKey string
	IASecretKey string

	// BrandFetch optional credentials for brand_research. Tool degrades gracefully when absent.
	BrandFetchAPIKey   string // BRANDFETCH_API_KEY — Brand API + Context API (Bearer auth)
	BrandFetchClientID string // BRANDFETCH_CLIENT_ID — Brand Search API (company_name→domain) + logo CDN requests

	// EcosystemsAPIKey is optional for awesome_list_search. ecosyste.ms's Free
	// plan (which self-service keys are issued under) uses the shared
	// "common"/"polite" pools, not API-key auth — API-key auth only takes
	// effect on ecosyste.ms's paid Develop/Scale plans. The key is still sent
	// (forward-compatible; a no-op on the Free plan). EcosystemsEmail opts
	// into the "polite pool" (mailto= — a real, verified rate-limit increase
	// on the Free plan) and falls back to OpenAlexEmail when unset.
	EcosystemsAPIKey string // ECOSYSTEMS_API_KEY
	EcosystemsEmail  string // falls back to OpenAlexEmail

	// GitHubToken is optional for the GitHub search provider, the awesome-list
	// GitHub topic-search fallback, and native GitHub content reading in
	// scrape_page (#282, #394, #395). Every one of those surfaces already
	// works unauthenticated (GitHub's own public rate limits); a token only
	// raises the ceiling, it never gates functionality. Never logged.
	GitHubToken string // GITHUB_TOKEN
}

type OAuthConfig struct {
	IssuerURL           string
	Audience            string
	JWKSRefreshInterval time.Duration // Default: 1 hour
	EnforceScopes       bool
	RequiredScopes      []string
}

// CircuitConfig holds the global default circuit-breaker thresholds (#468),
// applied at every circuit.New() call site in cmd/web-researcher-mcp/main.go
// and internal/search unless that site documents a specific override (e.g.
// Monarch's longer reset for its documented multi-minute KG-rebuild outages).
type CircuitConfig struct {
	FailureThreshold int
	ResetTimeoutSecs int
}

type RateLimitConfig struct {
	PerTenant  int
	Global     int
	DailyQuota int
	PerIP      int
	TrustProxy bool
	Persist    bool
	// MaxCallsPerDay, when >0, caps total tool calls per (tenant,user) per UTC
	// day IN-PROCESS — a transport-agnostic denial-of-wallet backstop that also
	// applies in STDIO (where the HTTP rate limits / DailyQuota don't run).
	// 0 (default) disables it. See MAX_CALLS_PER_DAY in .env.example.
	MaxCallsPerDay int
}

// ResearchPanelConfig holds credentials/endpoints for the research_panel tool
// (#302). Every field is optional; AvailableModelProviders() auto-detects a
// panel from whatever is set and degrades gracefully to an empty panel
// (tool not registered) when nothing is configured.
type ResearchPanelConfig struct {
	OpenRouterAPIKey string // OPENROUTER_API_KEY — unlocks 400+ models via one key, highest auto-detect priority
	OpenAIAPIKey     string // OPENAI_API_KEY — direct OpenAI
	AnthropicAPIKey  string // ANTHROPIC_API_KEY — direct Anthropic
	GoogleAIAPIKey   string // GOOGLE_AI_API_KEY — direct Google Gemini
	OllamaBaseURL    string // OLLAMA_BASE_URL — local Ollama, requires AllowPrivateIPs unless pointed at a public host
	LMStudioBaseURL  string // LM_STUDIO_BASE_URL — local LM Studio, same gating as Ollama
	DefaultModels    []string
	MaxModels        int

	// Per-provider default-model overrides for the auto-detected panel (no
	// RESEARCH_PANEL_DEFAULT_MODELS set). Each is optional; empty falls back
	// to the built-in default in research_panel_providers.go. Use
	// RESEARCH_PANEL_DEFAULT_MODELS instead to override the whole panel at once.
	OpenAIModel              string // RESEARCH_PANEL_OPENAI_MODEL
	AnthropicModel           string // RESEARCH_PANEL_ANTHROPIC_MODEL
	GoogleModel              string // RESEARCH_PANEL_GOOGLE_MODEL
	BedrockModel             string // RESEARCH_PANEL_BEDROCK_MODEL
	OllamaModel              string // RESEARCH_PANEL_OLLAMA_MODEL
	LMStudioModel            string // RESEARCH_PANEL_LMSTUDIO_MODEL
	OpenRouterAnthropicModel string // RESEARCH_PANEL_OPENROUTER_ANTHROPIC_MODEL
	OpenRouterOpenAIModel    string // RESEARCH_PANEL_OPENROUTER_OPENAI_MODEL
	OpenRouterGoogleModel    string // RESEARCH_PANEL_OPENROUTER_GOOGLE_MODEL

	// Cost tracking (#303). All optional; zero values disable the
	// corresponding guard so cost tracking stays entirely opt-in.
	PriceTablePath  string  // RESEARCH_PANEL_PRICE_TABLE_PATH — operator-managed JSON {"model_id":{"input_per_1k":0.003,"output_per_1k":0.015}}
	MaxCallCostUSD  float64 // RESEARCH_PANEL_MAX_CALL_COST_USD — per-call pre-flight hard cap; 0 = no cap
	MaxDailyCostUSD float64 // RESEARCH_PANEL_MAX_DAILY_COST_USD — per-tenant daily spend cap; 0 = no cap
	DryRun          bool    // RESEARCH_PANEL_DRY_RUN — return cost estimates only, call no models
}

func Load() (*Config, error) {
	var errs []string

	googleKey := os.Getenv("GOOGLE_CUSTOM_SEARCH_API_KEY")
	googleCX := os.Getenv("GOOGLE_CUSTOM_SEARCH_ID")
	routing := os.Getenv("SEARCH_ROUTING")
	providerEnv := os.Getenv("SEARCH_PROVIDER") // raw; empty when unset

	// Google keys are required ONLY when the user explicitly selects the Google
	// provider (SEARCH_PROVIDER=google) without multi-provider routing. When
	// SEARCH_PROVIDER is unset or any other value, the server starts keyless:
	// search.NewProvider falls back to the zero-config DuckDuckGo provider. This
	// matches the documented contract ("Required: None — DuckDuckGo works as a
	// zero-config fallback"); requiring Google keys for an unset provider would
	// make that promise impossible to honor (e.g. `docker run -e PORT=...`).
	if routing == "" && providerEnv == "google" {
		if googleKey == "" {
			errs = append(errs, "GOOGLE_CUSTOM_SEARCH_API_KEY is required when SEARCH_PROVIDER=google")
		}
		if googleCX == "" {
			errs = append(errs, "GOOGLE_CUSTOM_SEARCH_ID is required when SEARCH_PROVIDER=google")
		}
	}

	provider := envOrDefault("SEARCH_PROVIDER", "google")
	braveKey := os.Getenv("BRAVE_API_KEY")
	if provider == "brave" && braveKey == "" {
		errs = append(errs, "BRAVE_API_KEY is required when SEARCH_PROVIDER=brave")
	}
	braveExtraSnippets := envBool("BRAVE_EXTRA_SNIPPETS", false)

	serperKey := os.Getenv("SERPER_API_KEY")
	if provider == "serper" && serperKey == "" {
		errs = append(errs, "SERPER_API_KEY is required when SEARCH_PROVIDER=serper")
	}

	searxngURL := os.Getenv("SEARXNG_URL")
	if provider == "searxng" && searxngURL == "" {
		errs = append(errs, "SEARXNG_URL is required when SEARCH_PROVIDER=searxng")
	}

	// AUDIT_WEBHOOK_URL (#466 half 2): optional SIEM export target. Validated
	// at this boundary (operator-supplied env var) rather than deferred to the
	// audit package, matching the fail-closed pattern used for other optional
	// URL-shaped config above — a malformed value refuses startup rather than
	// silently never firing.
	auditWebhookURL := os.Getenv("AUDIT_WEBHOOK_URL")
	if auditWebhookURL != "" {
		if u, perr := url.Parse(auditWebhookURL); perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, "AUDIT_WEBHOOK_URL must be a valid absolute http(s) URL")
		}
	}

	// SearXNG auth (optional). Parsed unconditionally — not gated on
	// SEARCH_PROVIDER=searxng — because SearXNG can also be a SEARCH_ROUTING or
	// fallback target. Malformed values are fail-closed (append to errs so the
	// server refuses to start) with redacted messages that never echo the
	// credential or a header value.
	searxngBasicAuth := os.Getenv("SEARXNG_BASIC_AUTH")
	if searxngBasicAuth != "" {
		if _, _, ok := splitSearXNGBasicAuth(searxngBasicAuth); !ok {
			errs = append(errs, "SEARXNG_BASIC_AUTH must be in 'user:password' form (non-empty username and password, single ':' delimiter)")
		}
	}
	searxngHeaders, hdrErrs := parseSearXNGHeaders(os.Getenv("SEARXNG_HEADERS"))
	errs = append(errs, hdrErrs...)

	searchAPIKey := os.Getenv("SEARCHAPI_API_KEY")
	if provider == "searchapi" && searchAPIKey == "" {
		errs = append(errs, "SEARCHAPI_API_KEY is required when SEARCH_PROVIDER=searchapi")
	}

	youDotComKey := os.Getenv("YOUDOTCOM_API_KEY")
	if provider == "youcom" && youDotComKey == "" {
		errs = append(errs, "YOUDOTCOM_API_KEY is required when SEARCH_PROVIDER=youcom")
	}

	tavilyKey := os.Getenv("TAVILY_API_KEY")
	if provider == "tavily" && tavilyKey == "" {
		errs = append(errs, "TAVILY_API_KEY is required when SEARCH_PROVIDER=tavily")
	}

	exaKey := os.Getenv("EXA_API_KEY")
	if provider == "exa" && exaKey == "" {
		errs = append(errs, "EXA_API_KEY is required when SEARCH_PROVIDER=exa")
	}

	xquikKey := os.Getenv("XQUIK_API_KEY")
	if provider == "xquik" && xquikKey == "" {
		errs = append(errs, "XQUIK_API_KEY is required when SEARCH_PROVIDER=xquik")
	}

	// Parallel is keyless, so the opt-in toggle, not a credential, is what
	// authorizes egress to it. Fail closed when it is selected without one.
	parallelEnabled := envBool("PARALLEL_ENABLED", false)
	if provider == "parallel" && !parallelEnabled {
		errs = append(errs, "PARALLEL_ENABLED=true is required when SEARCH_PROVIDER=parallel")
	}

	port := envInt("PORT", 0)
	encKey := os.Getenv("CACHE_ENCRYPTION_KEY")
	if encKey != "" && !isHex64(encKey) {
		errs = append(errs, "CACHE_ENCRYPTION_KEY must be exactly 64 hex characters")
	}

	encKeyPrev := os.Getenv("CACHE_ENCRYPTION_KEY_PREV")
	if encKeyPrev != "" && !isHex64(encKeyPrev) {
		errs = append(errs, "CACHE_ENCRYPTION_KEY_PREV must be exactly 64 hex characters")
	}

	// Cookie vault (#241): the vault key protects session credentials, so it must
	// never equal a cache key (a cache-key leak or rotation must not expose or
	// invalidate stored logins).
	cookieVaultEnabled := envBool("COOKIE_VAULT_ENABLED", false)
	cookieVaultKey := os.Getenv("COOKIE_VAULT_ENCRYPTION_KEY")
	cookieVaultTTL := envDuration("COOKIE_VAULT_TTL", maxCookieVaultTTLDefault)
	maxCaptures := envInt("MAX_CONCURRENT_CAPTURES", 1)
	captureTimeout := envDuration("COOKIE_CAPTURE_TIMEOUT", 5*time.Minute)
	profileTTL := envDuration("COOKIE_VAULT_PROFILE_TTL", defaultProfileTTL)
	profileMaxTotalMB := envInt("COOKIE_VAULT_PROFILE_MAX_TOTAL_MB", defaultProfileMaxTotalMB)
	if cookieVaultEnabled {
		switch {
		case cookieVaultKey == "":
			errs = append(errs, "COOKIE_VAULT_ENCRYPTION_KEY is required when COOKIE_VAULT_ENABLED=true")
		case !isHex64(cookieVaultKey):
			errs = append(errs, "COOKIE_VAULT_ENCRYPTION_KEY must be exactly 64 hex characters")
		case strings.EqualFold(cookieVaultKey, encKey) || strings.EqualFold(cookieVaultKey, encKeyPrev):
			errs = append(errs, "COOKIE_VAULT_ENCRYPTION_KEY must differ from CACHE_ENCRYPTION_KEY")
		}
		if cookieVaultTTL <= 0 || cookieVaultTTL > maxCookieVaultTTL {
			errs = append(errs, "COOKIE_VAULT_TTL must be between 1s and 720h")
		}
		if profileTTL <= 0 || profileTTL > maxCookieVaultTTL {
			errs = append(errs, "COOKIE_VAULT_PROFILE_TTL must be between 1s and 720h")
		}
		if profileMaxTotalMB < 64 || profileMaxTotalMB > 102400 {
			errs = append(errs, "COOKIE_VAULT_PROFILE_MAX_TOTAL_MB must be between 64 and 102400")
		}
		if maxCaptures < 1 || maxCaptures > 5 {
			errs = append(errs, "MAX_CONCURRENT_CAPTURES must be between 1 and 5")
		}
		if captureTimeout < 30*time.Second || captureTimeout > 15*time.Minute {
			errs = append(errs, "COOKIE_CAPTURE_TIMEOUT must be between 30s and 15m")
		}
	}

	// ADMIN_API_KEY gates every /admin/* endpoint (cache flush, session flush,
	// GDPR data-subject rights, tenant analytics, workspace membership). The
	// legacy CACHE_ADMIN_KEY name is still accepted for backward compatibility
	// but deprecated, since the key is no longer cache-specific.
	var warnings []string
	adminKey := os.Getenv("ADMIN_API_KEY")
	adminKeyVar := "ADMIN_API_KEY"
	if adminKey == "" {
		if legacy := os.Getenv("CACHE_ADMIN_KEY"); legacy != "" {
			adminKey = legacy
			adminKeyVar = "CACHE_ADMIN_KEY"
			warnings = append(warnings, "CACHE_ADMIN_KEY is deprecated; rename it to ADMIN_API_KEY (the key now gates all /admin/* endpoints, not just cache). CACHE_ADMIN_KEY still works for now.")
		}
	} else if os.Getenv("CACHE_ADMIN_KEY") != "" {
		warnings = append(warnings, "Both ADMIN_API_KEY and CACHE_ADMIN_KEY are set; ADMIN_API_KEY takes precedence. Remove the deprecated CACHE_ADMIN_KEY.")
	}
	// Insecure-default notice (ASI10): HTTP transport exposed without an OAuth
	// issuer means every request runs unauthenticated as tenant=default/user=
	// anonymous. The gate behavior is intentional (zero-config), but a silent
	// start hides it — warn loudly so an operator never exposes it unknowingly.
	if port > 0 && os.Getenv("OAUTH_ISSUER_URL") == "" {
		warnings = append(warnings, "HTTP transport is enabled (PORT set) without OAUTH_ISSUER_URL — all requests run UNAUTHENTICATED as tenant=default/user=anonymous. Set OAUTH_ISSUER_URL for authenticated multi-tenant use.")
	}
	// CACHE_ISOLATION footgun guard (#484): CacheIsolation defaults to "shared"
	// for the zero-config/single-tenant case, but that default is silent and
	// dangerous once real tenant auth is active — an operator who never heard
	// of the flag gets a cache that mixes cached responses across tenants. Once
	// OAuth is configured (multi-tenant HTTP mode), require CACHE_ISOLATION to
	// be set explicitly (either value) so the choice is deliberate, not assumed.
	if port > 0 && os.Getenv("OAUTH_ISSUER_URL") != "" && os.Getenv("CACHE_ISOLATION") == "" {
		errs = append(errs, "CACHE_ISOLATION must be set explicitly when OAUTH_ISSUER_URL is configured (multi-tenant HTTP mode) — set CACHE_ISOLATION=tenant to scope cache keys per tenant, or CACHE_ISOLATION=shared to explicitly accept that cached responses are shared across tenants")
	}
	if adminKey != "" && len(adminKey) < 16 {
		errs = append(errs, adminKeyVar+" must be at least 16 characters")
	}

	// ADMIN_API_KEY_PREV (#488): the same zero-downtime rotation pattern as
	// CACHE_ENCRYPTION_KEY_PREV, but for the stateless admin key — accept
	// either the current or previous key for a grace window so a rotation
	// never produces a hard cutover that locks out in-flight automation.
	adminKeyPrev := os.Getenv("ADMIN_API_KEY_PREV")
	if adminKeyPrev != "" {
		if len(adminKeyPrev) < 16 {
			errs = append(errs, "ADMIN_API_KEY_PREV must be at least 16 characters")
		}
		if adminKey == "" {
			warnings = append(warnings, "ADMIN_API_KEY_PREV is set without ADMIN_API_KEY; it has no effect since admin endpoints are disabled")
		}
	}

	// STDIO single-user identity (opt-in). Validated, never fatal: a bad value
	// degrades to unset + a warning, preserving zero-config startup. Only honored
	// in STDIO (port==0); whenever PORT is set we leave it
	// empty and warn that the var is ignored.
	stdioUserID := ""
	if raw := os.Getenv("STDIO_USER_ID"); raw != "" {
		if v, ok := validateStdioUserID(raw); ok {
			if port > 0 {
				warnings = append(warnings, "STDIO_USER_ID is ignored whenever PORT is set (HTTP mode) — HTTP identity comes from OAuth when configured, or is anonymous in the unauthenticated fallback.")
			} else {
				stdioUserID = v
			}
		} else {
			warnings = append(warnings, `STDIO_USER_ID is invalid (allowed: A-Za-z0-9._@-, length 1-128, not "anonymous"); ignoring it.`)
		}
	}

	// COOKIE_VAULT_STDIO_CONSENT (#241): explicit operator opt-in to grant the
	// session_capture consent to STDIO_USER_ID at startup. STDIO has no consent
	// endpoint and cannot ask mid-request, so this is the only way to grant it.
	// Never defaulted on; ignored (with a warning) unless it can take effect.
	cookieVaultStdioConsent := envBool("COOKIE_VAULT_STDIO_CONSENT", false)
	if cookieVaultStdioConsent && (!cookieVaultEnabled || stdioUserID == "") {
		warnings = append(warnings, "COOKIE_VAULT_STDIO_CONSENT is ignored: it needs COOKIE_VAULT_ENABLED=true, STDIO mode (no PORT) and a valid STDIO_USER_ID.")
		cookieVaultStdioConsent = false
	}

	// COOKIE_CAPTURE_IN_HTTP (#775): site_login in HTTP mode opens a visible
	// window on the server host, so it needs the vault, HTTP mode and a display.
	// Never defaulted on; ignored (with a warning) unless it can take effect.
	cookieCaptureInHTTP := envBool("COOKIE_CAPTURE_IN_HTTP", false)
	if cookieCaptureInHTTP {
		switch {
		case !cookieVaultEnabled:
			warnings = append(warnings, "COOKIE_CAPTURE_IN_HTTP is ignored: it needs COOKIE_VAULT_ENABLED=true.")
			cookieCaptureInHTTP = false
		case !hasDisplay():
			warnings = append(warnings, "COOKIE_CAPTURE_IN_HTTP is ignored: this host has no display (set DISPLAY or WAYLAND_DISPLAY). Run site_login from a STDIO session instead.")
			cookieCaptureInHTTP = false
		}
	}

	// AUDIT_RETENTION_DAYS: 0 disables cleanup; any other value is clamped to
	// [180,3650] per NIS2/HGB retention floors.
	retentionDays := envInt("AUDIT_RETENTION_DAYS", 180)
	if retentionDays != 0 {
		if retentionDays < 180 {
			retentionDays = 180
		} else if retentionDays > 3650 {
			retentionDays = 3650
		}
	}

	logLevel := parseLogLevel(envOrDefault("LOG_LEVEL", "info"))

	cfg := &Config{
		GoogleAPIKey: googleKey,
		GoogleCX:     googleCX,
		Search: SearchConfig{
			Provider:              provider,
			Routing:               os.Getenv("SEARCH_ROUTING"),
			GoogleAPIKey:          googleKey,
			GoogleCX:              googleCX,
			BraveAPIKey:           braveKey,
			BraveExtraSnippets:    braveExtraSnippets,
			SerperAPIKey:          serperKey,
			SearchAPIKey:          searchAPIKey,
			YouDotComAPIKey:       youDotComKey,
			TavilyAPIKey:          tavilyKey,
			ExaAPIKey:             exaKey,
			XQuikAPIKey:           xquikKey,
			ParallelEnabled:       parallelEnabled,
			ParallelAPIKey:        os.Getenv("PARALLEL_API_KEY"),
			JinaAPIKey:            os.Getenv("JINA_API_KEY"),
			SearXNGURL:            searxngURL,
			SearXNGBasicAuth:      searxngBasicAuth,
			SearXNGHeaders:        searxngHeaders,
			CustomLensesPath:      os.Getenv("CUSTOM_LENSES_PATH"),
			ThinThreshold:         envInt("SEARCH_THIN_THRESHOLD", 1),
			USPTOAPIKey:           os.Getenv("USPTO_API_KEY"),
			EPOConsumerKey:        os.Getenv("EPO_OPS_CONSUMER_KEY"),
			EPOConsumerSecret:     os.Getenv("EPO_OPS_CONSUMER_SECRET"),
			LensAPIToken:          os.Getenv("LENS_API_TOKEN"),
			OpenAlexEmail:         os.Getenv("OPENALEX_EMAIL"),
			CrossRefEmail:         os.Getenv("CROSSREF_EMAIL"),
			SemanticScholarAPIKey: os.Getenv("SEMANTIC_SCHOLAR_API_KEY"),
			UnpaywallEmail:        envOrDefault("UNPAYWALL_EMAIL", os.Getenv("OPENALEX_EMAIL")),
			PubMedAPIKey:          os.Getenv("PUBMED_API_KEY"),
			PubMedEmail:           envOrDefault("PUBMED_EMAIL", os.Getenv("OPENALEX_EMAIL")),
			COREAPIKey:            os.Getenv("CORE_API_KEY"),
			ScholarAPIKey:         os.Getenv("SCHOLAR_API_KEY"),
			EDGARContactEmail:     envOrDefault("EDGAR_CONTACT_EMAIL", os.Getenv("OPENALEX_EMAIL")),
			CourtListenerToken:    os.Getenv("COURTLISTENER_API_TOKEN"),
			FREDAPIKey:            os.Getenv("FRED_API_KEY"),
			IAAccessKey:           os.Getenv("IA_ACCESS_KEY"),
			IASecretKey:           os.Getenv("IA_SECRET_KEY"),
			BrandFetchAPIKey:      os.Getenv("BRANDFETCH_API_KEY"),
			BrandFetchClientID:    os.Getenv("BRANDFETCH_CLIENT_ID"),
			EcosystemsAPIKey:      os.Getenv("ECOSYSTEMS_API_KEY"),
			EcosystemsEmail:       envOrDefault("ECOSYSTEMS_EMAIL", os.Getenv("OPENALEX_EMAIL")),
			GitHubToken:           os.Getenv("GITHUB_TOKEN"),
		},
		Port: port,
		OAuth: OAuthConfig{
			IssuerURL:           os.Getenv("OAUTH_ISSUER_URL"),
			Audience:            os.Getenv("OAUTH_AUDIENCE"),
			JWKSRefreshInterval: envDuration("JWKS_REFRESH_INTERVAL", 1*time.Hour),
			EnforceScopes:       envBool("ENFORCE_SCOPES", false),
			RequiredScopes:      splitCSV(os.Getenv("REQUIRED_SCOPES")),
		},
		AllowedOrigins: splitCSV(os.Getenv("ALLOWED_ORIGINS")),
		// Fail-closed by default: an empty ALLOWED_ORIGINS denies all cross-origin
		// browser requests. Operators set ALLOWED_ORIGINS to their client's origin,
		// or CORS_STRICT=false to restore the legacy reflect-any-Origin behavior.
		// HTTP/browser-only — STDIO and backend-to-backend clients are unaffected.
		CORSStrict: envBool("CORS_STRICT", true),
		HTTP: HTTPConfig{
			ReadHeaderTimeout: envDuration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
			ReadTimeout:       envDuration("HTTP_READ_TIMEOUT", 30*time.Second),
			WriteTimeout:      envDuration("HTTP_WRITE_TIMEOUT", 0),
			IdleTimeout:       envDuration("HTTP_IDLE_TIMEOUT", 120*time.Second),
			ShutdownTimeout:   envDuration("HTTP_SHUTDOWN_TIMEOUT", 30*time.Second),
			MaxHeaderBytes:    envInt("HTTP_MAX_HEADER_BYTES", 1<<20),
			MaxRequestBody:    envInt("MAX_REQUEST_BODY_BYTES", 10<<20),
			CSP:               envOrDefault("HTTP_CSP", "default-src 'none'; frame-ancestors 'none'"),
			ReferrerPolicy:    envOrDefault("HTTP_REFERRER_POLICY", "no-referrer"),
			PermissionsPolicy: envOrDefault("HTTP_PERMISSIONS_POLICY", "geolocation=(), camera=(), microphone=()"),
		},
		CacheDir:                     envOrDefault("CACHE_DIR", defaultCacheDir()),
		CacheMaxMemoryMB:             envInt("CACHE_MAX_MEMORY_MB", 64),
		CacheEncryptionKey:           encKey,
		CacheEncryptionKeyPrev:       encKeyPrev,
		CookieVaultKey:               cookieVaultKey,
		CookieVaultTTL:               cookieVaultTTL,
		MaxConcurrentCaptures:        maxCaptures,
		CookieCaptureTimeout:         captureTimeout,
		CookieVaultProfileTTL:        profileTTL,
		CookieVaultProfileMaxTotalMB: profileMaxTotalMB,
		CookieCaptureInHTTP:          cookieCaptureInHTTP,
		CookieVaultStdioConsent:      cookieVaultStdioConsent,
		CacheIsolation:               envOrDefault("CACHE_ISOLATION", "shared"),
		RedisURL:                     os.Getenv("REDIS_URL"),
		RateLimit: RateLimitConfig{
			PerTenant:      envInt("RATE_LIMIT_PER_TENANT", 120),
			Global:         envInt("RATE_LIMIT_GLOBAL", 1000),
			DailyQuota:     envInt("DAILY_QUOTA_PER_TENANT", 5000),
			PerIP:          envInt("RATE_LIMIT_PER_IP", 0),
			TrustProxy:     envBool("TRUST_PROXY", false),
			Persist:        envBool("RATE_LIMIT_PERSIST", false),
			MaxCallsPerDay: envInt("MAX_CALLS_PER_DAY", 0),
		},
		AllowPrivateIPs: envBool("ALLOW_PRIVATE_IPS", false),
		AllowedDomains:  splitCSV(os.Getenv("ALLOWED_DOMAINS")),
		ChromePath:      os.Getenv("CHROME_PATH"),
		ChromeNoSandbox: envBool("CHROME_NO_SANDBOX", false),
		JinaDisabled:    envBool("JINA_READER_DISABLED", false),
		// BrowserIdleTimeout closes the browser-tier Chromium process after this
		// long with no scrapeBrowser/ExtractLinks call (#460) — without it, one
		// browser-tier scrape pins a Chromium process (and its macOS Dock icon)
		// for the full server lifetime. Explicit "0" disables the idle-close
		// timer entirely (env unset ⇒ the 5-minute default; env="0" ⇒ disabled).
		BrowserIdleTimeout:    envDuration("BROWSER_IDLE_TIMEOUT", 5*time.Minute),
		MaxScrapeConcurrency:  envInt("MAX_SCRAPE_CONCURRENCY", 5),
		MaxBrowserConcurrency: envInt("MAX_SCRAPE_CONCURRENCY_BROWSER", 2),
		// MaxScrapeConcurrencyPerTenant defaults to the global MaxScrapeConcurrency
		// itself (#463): unset ⇒ "no per-tenant restriction beyond the global
		// cap" — a single-tenant deployment (the common case) sees no behavior
		// change. In a shared multi-tenant deployment, set this below the global
		// cap so one tenant's burst can't occupy every slot.
		MaxScrapeConcurrencyPerTenant: envInt("MAX_SCRAPE_CONCURRENCY_PER_TENANT", envInt("MAX_SCRAPE_CONCURRENCY", 5)),
		MaxHTMLBytes:                  envInt("MAX_HTML_BYTES", 8<<20),
		MaxDocumentBytes:              envInt("MAX_DOCUMENT_BYTES", 50<<20),
		SessionTTL:                    envDuration("SESSION_TTL", 4*time.Hour),
		SessionDataDir:                envOrDefault("SESSION_DATA_DIR", filepath.Join(envOrDefault("CACHE_DIR", defaultCacheDir()), "sessions")),
		SessionMaxSteps:               envInt("SESSION_MAX_STEPS", 200),
		LogLevel:                      logLevel,
		LogFormat:                     envOrDefault("LOG_FORMAT", "json"),
		MetricsEnabled:                envBool("METRICS_ENABLED", true),
		MetricsMaxTenants:             envInt("METRICS_MAX_TENANTS", 10000),
		AdminAPIKey:                   adminKey,
		AdminAPIKeyPrev:               adminKeyPrev,
		DataRegion:                    os.Getenv("DATA_REGION"),
		StdioUserID:                   stdioUserID,
		Features: FeatureConfig{
			SourceRecommendations: envBool("SOURCE_RECOMMENDATIONS", true),
			GenerativeUI:          envBool("GENERATIVE_UI_ENABLED", false),
			Memory:                envBool("MEMORY_ENABLED", false),
			UserAnalytics:         envBool("USER_ANALYTICS_ENABLED", false),
			Workspaces:            envBool("WORKSPACES_ENABLED", false),
			Monitoring:            envBool("MONITORING_ENABLED", false),
			CookieVault:           cookieVaultEnabled,
			MemoryRetention:       envDuration("MEMORY_RETENTION", 90*24*time.Hour),
			WorkspaceTTL:          envDuration("WORKSPACE_TTL", 30*24*time.Hour),
			MonitoringTTL:         envDuration("MONITORING_TTL", 30*24*time.Hour),
		},
		Audit: AuditConfig{
			Enabled:            envBool("AUDIT_ENABLED", true),
			OutputPath:         os.Getenv("AUDIT_OUTPUT_PATH"),
			BufferSize:         envInt("AUDIT_BUFFER_SIZE", 1000),
			IncludeRequestBody: envBool("AUDIT_INCLUDE_REQUEST_BODY", false),
			MaxBytes:           envInt("AUDIT_MAX_BYTES", 100<<20),
			RetentionDays:      retentionDays,
			WebhookURL:         auditWebhookURL,
		},
		Circuit: CircuitConfig{
			FailureThreshold: envInt("CIRCUIT_FAILURE_THRESHOLD", 5),
			ResetTimeoutSecs: envInt("CIRCUIT_RESET_TIMEOUT_SECONDS", 60),
		},
		ResearchPanel: ResearchPanelConfig{
			OpenRouterAPIKey: os.Getenv("OPENROUTER_API_KEY"),
			OpenAIAPIKey:     os.Getenv("OPENAI_API_KEY"),
			AnthropicAPIKey:  os.Getenv("ANTHROPIC_API_KEY"),
			GoogleAIAPIKey:   os.Getenv("GOOGLE_AI_API_KEY"),
			OllamaBaseURL:    os.Getenv("OLLAMA_BASE_URL"),
			LMStudioBaseURL:  os.Getenv("LM_STUDIO_BASE_URL"),
			DefaultModels:    splitCSV(os.Getenv("RESEARCH_PANEL_DEFAULT_MODELS")),
			MaxModels:        envInt("RESEARCH_PANEL_MAX_MODELS", 3),
			PriceTablePath:   os.Getenv("RESEARCH_PANEL_PRICE_TABLE_PATH"),
			MaxCallCostUSD:   envFloat("RESEARCH_PANEL_MAX_CALL_COST_USD", 0),
			MaxDailyCostUSD:  envFloat("RESEARCH_PANEL_MAX_DAILY_COST_USD", 0),
			DryRun:           envBool("RESEARCH_PANEL_DRY_RUN", false),

			OpenAIModel:              os.Getenv("RESEARCH_PANEL_OPENAI_MODEL"),
			AnthropicModel:           os.Getenv("RESEARCH_PANEL_ANTHROPIC_MODEL"),
			GoogleModel:              os.Getenv("RESEARCH_PANEL_GOOGLE_MODEL"),
			BedrockModel:             os.Getenv("RESEARCH_PANEL_BEDROCK_MODEL"),
			OllamaModel:              os.Getenv("RESEARCH_PANEL_OLLAMA_MODEL"),
			LMStudioModel:            os.Getenv("RESEARCH_PANEL_LMSTUDIO_MODEL"),
			OpenRouterAnthropicModel: os.Getenv("RESEARCH_PANEL_OPENROUTER_ANTHROPIC_MODEL"),
			OpenRouterOpenAIModel:    os.Getenv("RESEARCH_PANEL_OPENROUTER_OPENAI_MODEL"),
			OpenRouterGoogleModel:    os.Getenv("RESEARCH_PANEL_OPENROUTER_GOOGLE_MODEL"),
		},
		Warnings: warnings,
	}

	if len(errs) > 0 {
		return cfg, fmt.Errorf("config validation: %s", strings.Join(errs, "; "))
	}
	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// isHex64 reports whether s is exactly 64 lowercase/uppercase hex characters,
// matching the AES-256 key encoding used for CACHE_ENCRYPTION_KEY.
// Cookie vault bundle lifetime: 24h default (session cookies must not outlive a
// working day), hard-capped at 30 days (#241).
const (
	maxCookieVaultTTLDefault = 24 * time.Hour
	maxCookieVaultTTL        = 30 * 24 * time.Hour
	// Saved browser profile lifetime: a week, because a profile is signed in
	// once and reused (issue #775).
	defaultProfileTTL = 7 * 24 * time.Hour
	// defaultProfileMaxTotalMB matches cookievault.DefaultProfileMaxTotalBytes.
	defaultProfileMaxTotalMB = 2048
)

// hostGOOS is runtime.GOOS; a variable so tests can pose as another OS.
var hostGOOS = runtime.GOOS

// hasDisplay reports whether a visible browser window can open on this host.
// macOS and Windows always have one; Linux needs an X11 or Wayland session.
func hasDisplay() bool {
	if hostGOOS != "linux" {
		return true
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// validateStdioUserID trims and validates STDIO_USER_ID. It returns the cleaned
// value and ok=true only for a non-empty id of length 1-128 over [A-Za-z0-9._@-]
// that is not the reserved literal "anonymous" (which would defeat the
// fail-closed consent gate). The value flows into cache/session/consent/
// data-subject keys, so the charset is deliberately conservative.
func validateStdioUserID(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 128 || s == "anonymous" {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '@' || c == '-'
		if !ok {
			return "", false
		}
	}
	return s, true
}

// splitSearXNGBasicAuth splits "user:password" on the FIRST colon. ok is true
// only when a colon is present and both halves are non-empty. The password
// (everything after the first colon) is preserved verbatim, so passwords may
// contain colons.
func splitSearXNGBasicAuth(raw string) (user, pass string, ok bool) {
	user, pass, found := strings.Cut(raw, ":")
	if !found || user == "" || pass == "" {
		return "", "", false
	}
	return user, pass, true
}

// parseSearXNGHeaders parses "Name1: V1, Name2: V2" into a map. It splits on
// commas, each segment on its FIRST colon, trims ASCII space/tab around name
// and value, skips blank segments (tolerating trailing/doubled commas), and
// validates each name as an RFC 7230 token and each value as free of CR/LF/NUL
// — closing CRLF header-injection at the boundary. It returns the map plus
// redacted error strings (header NAME or shape only, never the value). Empty
// input => (nil, nil), the zero-config path identical to today. Names are
// canonicalized (textproto.CanonicalMIMEHeaderKey), so case-variant duplicates
// collapse to one entry; duplicate names: last wins, deterministic across
// restarts. An empty value is allowed (RFC 7230 permits empty header values).
func parseSearXNGHeaders(raw string) (map[string]string, []string) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	out := make(map[string]string)
	var errsOut []string
	for _, seg := range strings.Split(raw, ",") {
		if strings.TrimSpace(seg) == "" {
			continue // tolerate trailing/doubled commas
		}
		name, value, found := strings.Cut(seg, ":")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !found {
			errsOut = append(errsOut, "SEARXNG_HEADERS entry is missing a ':' delimiter")
			continue
		}
		if !isHTTPToken(name) {
			errsOut = append(errsOut, fmt.Sprintf("SEARXNG_HEADERS header name %q is not a valid HTTP token", name))
			continue
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			errsOut = append(errsOut, fmt.Sprintf("SEARXNG_HEADERS value for header %q contains a forbidden control character", name))
			continue
		}
		// Canonicalize the name (e.g. "x-api-key" -> "X-Api-Key") so dedup matches
		// the on-the-wire key http.Header.Set uses; otherwise case-variant
		// duplicates would collide nondeterministically via random map order.
		out[textproto.CanonicalMIMEHeaderKey(name)] = value
	}
	if len(out) == 0 {
		return nil, errsOut
	}
	return out, errsOut
}

// isHTTPToken reports whether s is a non-empty RFC 7230 header-name token.
func isHTTPToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func defaultCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "web-researcher-mcp")
	}
	return filepath.Join(os.TempDir(), "web-researcher-mcp-cache")
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
