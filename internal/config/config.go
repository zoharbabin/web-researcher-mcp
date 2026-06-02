package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	CacheIsolation         string
	RedisURL               string
	RateLimit              RateLimitConfig
	AllowPrivateIPs        bool
	AllowedDomains         []string
	ChromePath             string
	MaxScrapeConcurrency   int
	SessionTTL             time.Duration
	SessionDataDir         string
	SessionMaxSteps        int
	LogLevel               slog.Level
	LogFormat              string
	MetricsEnabled         bool
	AdminAPIKey            string
	DataRegion             string
	Features               FeatureConfig
	Audit                  AuditConfig

	// StdioUserID names the single local user for STDIO transport, where there
	// is no OAuth identity (the launching app owns the process, so it IS one
	// trusted user). When set, the server injects tenant=default/user=<value>
	// into STDIO request context, making the per-user regulated features
	// (memory, analytics) reachable. Empty (default) keeps the "anonymous"
	// behavior. Ignored in HTTP mode (identity comes from OAuth). Validated to a
	// safe charset since it flows into cache/session/consent/data-subject keys.
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

	// MemoryRetention bounds how long a saved memory lives before auto-expiry
	// (#88). 0 → the store's default (90 days). "Data doesn't exist after TTL"
	// stays the safety property unless the operator extends it.
	MemoryRetention time.Duration

	// WorkspaceTTL bounds how long shared-workspace data lives (#96).
	// 0 → the store's default (30 days).
	WorkspaceTTL time.Duration
}

// RegulatedEnabled reports whether any consent-gated feature is on, which is
// the sole trigger for activating the consent subsystem.
func (f FeatureConfig) RegulatedEnabled() bool {
	return f.Memory || f.UserAnalytics || f.Workspaces
}

type AuditConfig struct {
	Enabled            bool
	OutputPath         string
	BufferSize         int
	IncludeRequestBody bool
	MaxBytes           int
	RetentionDays      int
}

type SearchConfig struct {
	Provider         string
	FallbackProvider string
	Routing          string
	GoogleAPIKey     string
	GoogleCX         string
	BraveAPIKey      string
	SerperAPIKey     string
	SearchAPIKey     string
	SearXNGURL       string
	CustomLensesPath string

	// Patent-specific providers (optional, enables structured patent search)
	USPTOAPIKey       string
	EPOConsumerKey    string
	EPOConsumerSecret string
	LensAPIToken      string

	// Academic-specific providers (optional, enables structured scholarly search)
	OpenAlexEmail string
	CrossRefEmail string
}

type OAuthConfig struct {
	IssuerURL           string
	Audience            string
	JWKSRefreshInterval time.Duration // Default: 1 hour
	EnforceScopes       bool
	RequiredScopes      []string
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

	serperKey := os.Getenv("SERPER_API_KEY")
	if provider == "serper" && serperKey == "" {
		errs = append(errs, "SERPER_API_KEY is required when SEARCH_PROVIDER=serper")
	}

	searxngURL := os.Getenv("SEARXNG_URL")
	if provider == "searxng" && searxngURL == "" {
		errs = append(errs, "SEARXNG_URL is required when SEARCH_PROVIDER=searxng")
	}

	searchAPIKey := os.Getenv("SEARCHAPI_API_KEY")
	if provider == "searchapi" && searchAPIKey == "" {
		errs = append(errs, "SEARCHAPI_API_KEY is required when SEARCH_PROVIDER=searchapi")
	}

	port := envInt("PORT", 0)
	encKey := os.Getenv("CACHE_ENCRYPTION_KEY")
	if encKey != "" && len(encKey) != 64 {
		errs = append(errs, "CACHE_ENCRYPTION_KEY must be exactly 64 hex characters")
	}

	encKeyPrev := os.Getenv("CACHE_ENCRYPTION_KEY_PREV")
	if encKeyPrev != "" && !isHex64(encKeyPrev) {
		errs = append(errs, "CACHE_ENCRYPTION_KEY_PREV must be exactly 64 hex characters")
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
	if adminKey != "" && len(adminKey) < 16 {
		errs = append(errs, adminKeyVar+" must be at least 16 characters")
	}

	// STDIO single-user identity (opt-in). Validated, never fatal: a bad value
	// degrades to unset + a warning, preserving zero-config startup. Only honored
	// in STDIO (port==0); in HTTP mode identity comes from OAuth, so we leave it
	// empty and warn that the var is ignored.
	stdioUserID := ""
	if raw := os.Getenv("STDIO_USER_ID"); raw != "" {
		if v, ok := validateStdioUserID(raw); ok {
			if port > 0 {
				warnings = append(warnings, "STDIO_USER_ID is ignored in HTTP mode (PORT set) — identity comes from OAuth.")
			} else {
				stdioUserID = v
			}
		} else {
			warnings = append(warnings, `STDIO_USER_ID is invalid (allowed: A-Za-z0-9._@-, length 1-128, not "anonymous"); ignoring it.`)
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
			Provider:          provider,
			FallbackProvider:  os.Getenv("SEARCH_FALLBACK_PROVIDER"),
			Routing:           os.Getenv("SEARCH_ROUTING"),
			GoogleAPIKey:      googleKey,
			GoogleCX:          googleCX,
			BraveAPIKey:       braveKey,
			SerperAPIKey:      serperKey,
			SearchAPIKey:      searchAPIKey,
			SearXNGURL:        searxngURL,
			CustomLensesPath:  os.Getenv("CUSTOM_LENSES_PATH"),
			USPTOAPIKey:       os.Getenv("USPTO_API_KEY"),
			EPOConsumerKey:    os.Getenv("EPO_OPS_CONSUMER_KEY"),
			EPOConsumerSecret: os.Getenv("EPO_OPS_CONSUMER_SECRET"),
			LensAPIToken:      os.Getenv("LENS_API_TOKEN"),
			OpenAlexEmail:     os.Getenv("OPENALEX_EMAIL"),
			CrossRefEmail:     os.Getenv("CROSSREF_EMAIL"),
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
			MaxHeaderBytes:    envInt("HTTP_MAX_HEADER_BYTES", 1<<20),
			MaxRequestBody:    envInt("MAX_REQUEST_BODY_BYTES", 10<<20),
			CSP:               envOrDefault("HTTP_CSP", "default-src 'none'; frame-ancestors 'none'"),
			ReferrerPolicy:    envOrDefault("HTTP_REFERRER_POLICY", "no-referrer"),
			PermissionsPolicy: envOrDefault("HTTP_PERMISSIONS_POLICY", "geolocation=(), camera=(), microphone=()"),
		},
		CacheDir:               envOrDefault("CACHE_DIR", defaultCacheDir()),
		CacheMaxMemoryMB:       envInt("CACHE_MAX_MEMORY_MB", 64),
		CacheEncryptionKey:     encKey,
		CacheEncryptionKeyPrev: encKeyPrev,
		CacheIsolation:         envOrDefault("CACHE_ISOLATION", "shared"),
		RedisURL:               os.Getenv("REDIS_URL"),
		RateLimit: RateLimitConfig{
			PerTenant:      envInt("RATE_LIMIT_PER_TENANT", 120),
			Global:         envInt("RATE_LIMIT_GLOBAL", 1000),
			DailyQuota:     envInt("DAILY_QUOTA_PER_TENANT", 5000),
			PerIP:          envInt("RATE_LIMIT_PER_IP", 0),
			TrustProxy:     envBool("TRUST_PROXY", false),
			Persist:        envBool("RATE_LIMIT_PERSIST", false),
			MaxCallsPerDay: envInt("MAX_CALLS_PER_DAY", 0),
		},
		AllowPrivateIPs:      envBool("ALLOW_PRIVATE_IPS", false),
		AllowedDomains:       splitCSV(os.Getenv("ALLOWED_DOMAINS")),
		ChromePath:           os.Getenv("CHROME_PATH"),
		MaxScrapeConcurrency: envInt("MAX_SCRAPE_CONCURRENCY", 5),
		SessionTTL:           envDuration("SESSION_TTL", 4*time.Hour),
		SessionDataDir:       envOrDefault("SESSION_DATA_DIR", filepath.Join(envOrDefault("CACHE_DIR", defaultCacheDir()), "sessions")),
		SessionMaxSteps:      envInt("SESSION_MAX_STEPS", 200),
		LogLevel:             logLevel,
		LogFormat:            envOrDefault("LOG_FORMAT", "json"),
		MetricsEnabled:       envBool("METRICS_ENABLED", true),
		AdminAPIKey:          adminKey,
		DataRegion:           os.Getenv("DATA_REGION"),
		StdioUserID:          stdioUserID,
		Features: FeatureConfig{
			SourceRecommendations: envBool("SOURCE_RECOMMENDATIONS", true),
			GenerativeUI:          envBool("GENERATIVE_UI_ENABLED", false),
			Memory:                envBool("MEMORY_ENABLED", false),
			UserAnalytics:         envBool("USER_ANALYTICS_ENABLED", false),
			Workspaces:            envBool("WORKSPACES_ENABLED", false),
			MemoryRetention:       envDuration("MEMORY_RETENTION", 90*24*time.Hour),
			WorkspaceTTL:          envDuration("WORKSPACE_TTL", 30*24*time.Hour),
		},
		Audit: AuditConfig{
			Enabled:            envBool("AUDIT_ENABLED", true),
			OutputPath:         os.Getenv("AUDIT_OUTPUT_PATH"),
			BufferSize:         envInt("AUDIT_BUFFER_SIZE", 1000),
			IncludeRequestBody: envBool("AUDIT_INCLUDE_REQUEST_BODY", false),
			MaxBytes:           envInt("AUDIT_MAX_BYTES", 100<<20),
			RetentionDays:      retentionDays,
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
