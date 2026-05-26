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
	GoogleAPIKey         string
	GoogleCX             string
	Search               SearchConfig
	Port                 int
	OAuth                OAuthConfig
	AllowedOrigins       []string
	CacheDir             string
	CacheMaxMemoryMB     int
	CacheEncryptionKey   string
	CacheIsolation       string
	RedisURL             string
	RateLimit            RateLimitConfig
	AllowPrivateIPs      bool
	AllowedDomains       []string
	ChromePath           string
	MaxScrapeConcurrency int
	SessionTTL           time.Duration
	LogLevel             slog.Level
	LogFormat            string
	MetricsEnabled       bool
	CacheAdminKey        string
	Audit                AuditConfig
}

type AuditConfig struct {
	Enabled            bool
	OutputPath         string
	BufferSize         int
	IncludeRequestBody bool
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
}

type RateLimitConfig struct {
	PerTenant  int
	Global     int
	DailyQuota int
}

func Load() (*Config, error) {
	var errs []string

	googleKey := os.Getenv("GOOGLE_CUSTOM_SEARCH_API_KEY")
	googleCX := os.Getenv("GOOGLE_CUSTOM_SEARCH_ID")
	routing := os.Getenv("SEARCH_ROUTING")

	// Google keys are required unless SEARCH_ROUTING is configured (which enables
	// multi-provider mode where Google may not be the primary/only provider).
	if routing == "" {
		if googleKey == "" {
			errs = append(errs, "GOOGLE_CUSTOM_SEARCH_API_KEY is required")
		}
		if googleCX == "" {
			errs = append(errs, "GOOGLE_CUSTOM_SEARCH_ID is required")
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
		},
		AllowedOrigins:     splitCSV(os.Getenv("ALLOWED_ORIGINS")),
		CacheDir:           envOrDefault("CACHE_DIR", defaultCacheDir()),
		CacheMaxMemoryMB:   envInt("CACHE_MAX_MEMORY_MB", 64),
		CacheEncryptionKey: encKey,
		CacheIsolation:     envOrDefault("CACHE_ISOLATION", "shared"),
		RedisURL:           os.Getenv("REDIS_URL"),
		RateLimit: RateLimitConfig{
			PerTenant:  envInt("RATE_LIMIT_PER_TENANT", 120),
			Global:     envInt("RATE_LIMIT_GLOBAL", 1000),
			DailyQuota: envInt("DAILY_QUOTA_PER_TENANT", 5000),
		},
		AllowPrivateIPs:      envBool("ALLOW_PRIVATE_IPS", false),
		AllowedDomains:       splitCSV(os.Getenv("ALLOWED_DOMAINS")),
		ChromePath:           os.Getenv("CHROME_PATH"),
		MaxScrapeConcurrency: envInt("MAX_SCRAPE_CONCURRENCY", 5),
		SessionTTL:           30 * time.Minute,
		LogLevel:             logLevel,
		LogFormat:            envOrDefault("LOG_FORMAT", "json"),
		MetricsEnabled:       envBool("METRICS_ENABLED", true),
		CacheAdminKey:        os.Getenv("CACHE_ADMIN_KEY"),
		Audit: AuditConfig{
			Enabled:            envBool("AUDIT_ENABLED", true),
			OutputPath:         os.Getenv("AUDIT_OUTPUT_PATH"),
			BufferSize:         envInt("AUDIT_BUFFER_SIZE", 1000),
			IncludeRequestBody: envBool("AUDIT_INCLUDE_REQUEST_BODY", false),
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
