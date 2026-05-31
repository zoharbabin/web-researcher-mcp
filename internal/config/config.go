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
	CacheAdminKey          string
	DataRegion             string
	Audit                  AuditConfig
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

	adminKey := os.Getenv("CACHE_ADMIN_KEY")
	if adminKey != "" && len(adminKey) < 16 {
		errs = append(errs, "CACHE_ADMIN_KEY must be at least 16 characters")
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
		CORSStrict:     envBool("CORS_STRICT", false),
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
			PerTenant:  envInt("RATE_LIMIT_PER_TENANT", 120),
			Global:     envInt("RATE_LIMIT_GLOBAL", 1000),
			DailyQuota: envInt("DAILY_QUOTA_PER_TENANT", 5000),
			PerIP:      envInt("RATE_LIMIT_PER_IP", 0),
			TrustProxy: envBool("TRUST_PROXY", false),
			Persist:    envBool("RATE_LIMIT_PERSIST", false),
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
		CacheAdminKey:        adminKey,
		DataRegion:           os.Getenv("DATA_REGION"),
		Audit: AuditConfig{
			Enabled:            envBool("AUDIT_ENABLED", true),
			OutputPath:         os.Getenv("AUDIT_OUTPUT_PATH"),
			BufferSize:         envInt("AUDIT_BUFFER_SIZE", 1000),
			IncludeRequestBody: envBool("AUDIT_INCLUDE_REQUEST_BODY", false),
			MaxBytes:           envInt("AUDIT_MAX_BYTES", 100<<20),
			RetentionDays:      retentionDays,
		},
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
