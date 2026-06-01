package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "test-api-key")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "test-cx-id")
}

func TestLoadWithRequiredEnvVars(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error with required vars set, got: %v", err)
	}
	if cfg.GoogleAPIKey != "test-api-key" {
		t.Errorf("expected GoogleAPIKey=test-api-key, got %s", cfg.GoogleAPIKey)
	}
	if cfg.GoogleCX != "test-cx-id" {
		t.Errorf("expected GoogleCX=test-cx-id, got %s", cfg.GoogleCX)
	}
}

// Google keys are required only when the user explicitly opts into the Google
// provider (SEARCH_PROVIDER=google). These tests pin that contract.
func TestLoadMissingGoogleAPIKey(t *testing.T) {
	t.Setenv("SEARCH_PROVIDER", "google")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "test-cx-id")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when GOOGLE_CUSTOM_SEARCH_API_KEY is missing under SEARCH_PROVIDER=google")
	}
	if !strings.Contains(err.Error(), "GOOGLE_CUSTOM_SEARCH_API_KEY is required") {
		t.Errorf("expected error about missing API key, got: %v", err)
	}
}

func TestLoadMissingGoogleCX(t *testing.T) {
	t.Setenv("SEARCH_PROVIDER", "google")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "test-key")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when GOOGLE_CUSTOM_SEARCH_ID is missing under SEARCH_PROVIDER=google")
	}
	if !strings.Contains(err.Error(), "GOOGLE_CUSTOM_SEARCH_ID is required") {
		t.Errorf("expected error about missing CX, got: %v", err)
	}
}

func TestLoadMissingBothRequired(t *testing.T) {
	t.Setenv("SEARCH_PROVIDER", "google")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when both Google vars are missing under SEARCH_PROVIDER=google")
	}
	if !strings.Contains(err.Error(), "GOOGLE_CUSTOM_SEARCH_API_KEY is required") {
		t.Errorf("expected error about API key, got: %v", err)
	}
	if !strings.Contains(err.Error(), "GOOGLE_CUSTOM_SEARCH_ID is required") {
		t.Errorf("expected error about CX, got: %v", err)
	}
}

// TestLoadZeroConfigDuckDuckGo pins the documented contract: with no provider
// selected and no Google keys, the server loads cleanly (it falls back to the
// zero-config DuckDuckGo provider at runtime). This is the keyless startup path
// exercised by `docker run -e PORT=...`.
func TestLoadZeroConfigDuckDuckGo(t *testing.T) {
	t.Setenv("SEARCH_PROVIDER", "")
	t.Setenv("SEARCH_ROUTING", "")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected keyless zero-config load to succeed, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

// TestLoadExplicitDuckDuckGoNoKeys: explicitly selecting DuckDuckGo also needs
// no keys.
func TestLoadExplicitDuckDuckGoNoKeys(t *testing.T) {
	t.Setenv("SEARCH_PROVIDER", "duckduckgo")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "")

	if _, err := Load(); err != nil {
		t.Fatalf("expected SEARCH_PROVIDER=duckduckgo to load without keys, got: %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Search.Provider != "google" {
		t.Errorf("expected default provider=google, got %s", cfg.Search.Provider)
	}
	if cfg.Port != 0 {
		t.Errorf("expected default port=0, got %d", cfg.Port)
	}
	if cfg.CacheDir == "" || cfg.CacheDir == "./cache" {
		t.Errorf("expected platform-specific cache dir, got %q", cfg.CacheDir)
	}
	if cfg.CacheMaxMemoryMB != 64 {
		t.Errorf("expected default CacheMaxMemoryMB=64, got %d", cfg.CacheMaxMemoryMB)
	}
	if cfg.RateLimit.PerTenant != 120 {
		t.Errorf("expected default PerTenant=120, got %d", cfg.RateLimit.PerTenant)
	}
	if cfg.RateLimit.Global != 1000 {
		t.Errorf("expected default Global=1000, got %d", cfg.RateLimit.Global)
	}
	if cfg.RateLimit.DailyQuota != 5000 {
		t.Errorf("expected default DailyQuota=5000, got %d", cfg.RateLimit.DailyQuota)
	}
	if cfg.MaxScrapeConcurrency != 5 {
		t.Errorf("expected default MaxScrapeConcurrency=5, got %d", cfg.MaxScrapeConcurrency)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("expected default LogFormat=json, got %s", cfg.LogFormat)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("expected default LogLevel=Info, got %v", cfg.LogLevel)
	}
	if !cfg.MetricsEnabled {
		t.Error("expected default MetricsEnabled=true")
	}
	if cfg.AllowPrivateIPs {
		t.Error("expected default AllowPrivateIPs=false")
	}
	if cfg.CacheIsolation != "shared" {
		t.Errorf("expected default CacheIsolation=shared, got %s", cfg.CacheIsolation)
	}
}

func setCustomEnv(t *testing.T) {
	t.Helper()
	setRequiredEnv(t)
	envVars := map[string]string{
		"SEARCH_PROVIDER":        "brave",
		"BRAVE_API_KEY":          "brave-key-123",
		"PORT":                   "8080",
		"CACHE_DIR":              "/tmp/cache",
		"CACHE_MAX_MEMORY_MB":    "128",
		"RATE_LIMIT_PER_TENANT":  "60",
		"RATE_LIMIT_GLOBAL":      "2000",
		"DAILY_QUOTA_PER_TENANT": "5000",
		"MAX_SCRAPE_CONCURRENCY": "10",
		"LOG_LEVEL":              "debug",
		"LOG_FORMAT":             "text",
		"METRICS_ENABLED":        "false",
		"ALLOW_PRIVATE_IPS":      "true",
		"ALLOWED_ORIGINS":        "http://localhost:3000, https://example.com",
		"ALLOWED_DOMAINS":        "example.com,test.org",
		"REDIS_URL":              "redis://localhost:6379",
		"CHROME_PATH":            "/usr/bin/chromium",
		"OAUTH_ISSUER_URL":       "https://auth.example.com",
		"OAUTH_AUDIENCE":         "my-audience",
	}
	for k, v := range envVars {
		t.Setenv(k, v)
	}
}

func TestLoadCustomSearchProvider(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Search.Provider != "brave" {
		t.Errorf("expected provider=brave, got %s", cfg.Search.Provider)
	}
	if cfg.Search.BraveAPIKey != "brave-key-123" {
		t.Errorf("expected BraveAPIKey=brave-key-123, got %s", cfg.Search.BraveAPIKey)
	}
}

func TestLoadCustomServerConfig(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("expected port=8080, got %d", cfg.Port)
	}
}

func TestLoadCustomCacheConfig(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CacheDir != "/tmp/cache" {
		t.Errorf("expected CacheDir=/tmp/cache, got %s", cfg.CacheDir)
	}
	if cfg.CacheMaxMemoryMB != 128 {
		t.Errorf("expected CacheMaxMemoryMB=128, got %d", cfg.CacheMaxMemoryMB)
	}
	if cfg.RedisURL != "redis://localhost:6379" {
		t.Errorf("expected RedisURL=redis://localhost:6379, got %s", cfg.RedisURL)
	}
}

func TestLoadCustomRateLimits(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RateLimit.PerTenant != 60 {
		t.Errorf("expected PerTenant=60, got %d", cfg.RateLimit.PerTenant)
	}
	if cfg.RateLimit.Global != 2000 {
		t.Errorf("expected Global=2000, got %d", cfg.RateLimit.Global)
	}
	if cfg.RateLimit.DailyQuota != 5000 {
		t.Errorf("expected DailyQuota=5000, got %d", cfg.RateLimit.DailyQuota)
	}
}

func TestLoadCustomScrapingConfig(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxScrapeConcurrency != 10 {
		t.Errorf("expected MaxScrapeConcurrency=10, got %d", cfg.MaxScrapeConcurrency)
	}
	if cfg.ChromePath != "/usr/bin/chromium" {
		t.Errorf("expected ChromePath=/usr/bin/chromium, got %s", cfg.ChromePath)
	}
	if !cfg.AllowPrivateIPs {
		t.Error("expected AllowPrivateIPs=true")
	}
}

func TestLoadCustomObservability(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("expected LogLevel=Debug, got %v", cfg.LogLevel)
	}
	if cfg.LogFormat != "text" {
		t.Errorf("expected LogFormat=text, got %s", cfg.LogFormat)
	}
	if cfg.MetricsEnabled {
		t.Error("expected MetricsEnabled=false")
	}
}

func TestLoadCustomNetworkConfig(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[0] != "http://localhost:3000" || cfg.AllowedOrigins[1] != "https://example.com" {
		t.Errorf("unexpected AllowedOrigins: %v", cfg.AllowedOrigins)
	}
	if len(cfg.AllowedDomains) != 2 || cfg.AllowedDomains[0] != "example.com" || cfg.AllowedDomains[1] != "test.org" {
		t.Errorf("unexpected AllowedDomains: %v", cfg.AllowedDomains)
	}
}

func TestLoadCustomOAuth(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OAuth.IssuerURL != "https://auth.example.com" {
		t.Errorf("expected OAuth.IssuerURL=https://auth.example.com, got %s", cfg.OAuth.IssuerURL)
	}
	if cfg.OAuth.Audience != "my-audience" {
		t.Errorf("expected OAuth.Audience=my-audience, got %s", cfg.OAuth.Audience)
	}
}

func TestLoadBraveProviderMissingKey(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SEARCH_PROVIDER", "brave")
	t.Setenv("BRAVE_API_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when SEARCH_PROVIDER=brave without BRAVE_API_KEY")
	}
	if !strings.Contains(err.Error(), "BRAVE_API_KEY is required") {
		t.Errorf("expected error about missing BRAVE_API_KEY, got: %v", err)
	}
}

func TestLoadSerperProviderMissingKey(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SEARCH_PROVIDER", "serper")
	t.Setenv("SERPER_API_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when SEARCH_PROVIDER=serper without SERPER_API_KEY")
	}
	if !strings.Contains(err.Error(), "SERPER_API_KEY is required") {
		t.Errorf("expected error about missing SERPER_API_KEY, got: %v", err)
	}
}

func TestLoadSearXNGProviderMissingURL(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SEARCH_PROVIDER", "searxng")
	t.Setenv("SEARXNG_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when SEARCH_PROVIDER=searxng without SEARXNG_URL")
	}
	if !strings.Contains(err.Error(), "SEARXNG_URL is required") {
		t.Errorf("expected error about missing SEARXNG_URL, got: %v", err)
	}
}

func TestLoadSearchAPIProviderMissingKey(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SEARCH_PROVIDER", "searchapi")
	t.Setenv("SEARCHAPI_API_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when SEARCH_PROVIDER=searchapi without SEARCHAPI_API_KEY")
	}
	if !strings.Contains(err.Error(), "SEARCHAPI_API_KEY is required") {
		t.Errorf("expected error about missing SEARCHAPI_API_KEY, got: %v", err)
	}
}

func TestLoadSearchAPIProviderWithKey(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SEARCH_PROVIDER", "searchapi")
	t.Setenv("SEARCHAPI_API_KEY", "test-searchapi-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Search.Provider != "searchapi" {
		t.Errorf("expected provider=searchapi, got %s", cfg.Search.Provider)
	}
	if cfg.Search.SearchAPIKey != "test-searchapi-key" {
		t.Errorf("expected SearchAPIKey=test-searchapi-key, got %s", cfg.Search.SearchAPIKey)
	}
}

func TestLoadSearchRouting(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SEARCH_ROUTING", "brave,google,serper")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Search.Routing != "brave,google,serper" {
		t.Errorf("expected Routing='brave,google,serper', got %q", cfg.Search.Routing)
	}
}

func TestLoadSearchRoutingRelaxesGoogleRequirement(t *testing.T) {
	// When SEARCH_ROUTING is set, Google keys are not required
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "")
	t.Setenv("SEARCH_PROVIDER", "brave")
	t.Setenv("BRAVE_API_KEY", "brave-key")
	t.Setenv("SEARCH_ROUTING", "brave")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error with routing configured and no Google keys, got: %v", err)
	}
	if cfg.Search.Routing != "brave" {
		t.Errorf("expected Routing='brave', got %q", cfg.Search.Routing)
	}
}

func TestLoadCacheEncryptionKeyValidation(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("CACHE_ENCRYPTION_KEY", "tooshort")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid CACHE_ENCRYPTION_KEY length")
	}
	if !strings.Contains(err.Error(), "CACHE_ENCRYPTION_KEY must be exactly 64 hex characters") {
		t.Errorf("expected error about key length, got: %v", err)
	}
}

func TestLoadCacheEncryptionKeyValid(t *testing.T) {
	setRequiredEnv(t)
	validKey := strings.Repeat("ab", 32) // 64 hex chars
	t.Setenv("CACHE_ENCRYPTION_KEY", validKey)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error with valid encryption key: %v", err)
	}
	if cfg.CacheEncryptionKey != validKey {
		t.Errorf("expected encryption key to be set")
	}
}

func TestLogLevelParsing(t *testing.T) {
	tests := []struct {
		env      string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"unknown", slog.LevelInfo},
		{"", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run("LOG_LEVEL="+tt.env, func(t *testing.T) {
			setRequiredEnv(t)
			if tt.env != "" {
				t.Setenv("LOG_LEVEL", tt.env)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.LogLevel != tt.expected {
				t.Errorf("for LOG_LEVEL=%q expected %v, got %v", tt.env, tt.expected, cfg.LogLevel)
			}
		})
	}
}

func TestEnvIntFallback(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PORT", "not-a-number")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 0 {
		t.Errorf("expected port=0 (fallback) for invalid int, got %d", cfg.Port)
	}
}

func TestSplitCSVEmpty(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ALLOWED_ORIGINS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AllowedOrigins != nil {
		t.Errorf("expected nil for empty CSV, got %v", cfg.AllowedOrigins)
	}
}

func TestSplitCSVTrimsSpaces(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ALLOWED_ORIGINS", "  http://a.com  ,  http://b.com  , , ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Fatalf("expected 2 origins, got %d: %v", len(cfg.AllowedOrigins), cfg.AllowedOrigins)
	}
	if cfg.AllowedOrigins[0] != "http://a.com" {
		t.Errorf("expected first origin=http://a.com, got %s", cfg.AllowedOrigins[0])
	}
	if cfg.AllowedOrigins[1] != "http://b.com" {
		t.Errorf("expected second origin=http://b.com, got %s", cfg.AllowedOrigins[1])
	}
}

func TestLoadHTTPHardeningDefaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTP.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("expected default ReadHeaderTimeout=5s, got %v", cfg.HTTP.ReadHeaderTimeout)
	}
	if cfg.HTTP.ReadTimeout != 30*time.Second {
		t.Errorf("expected default ReadTimeout=30s, got %v", cfg.HTTP.ReadTimeout)
	}
	if cfg.HTTP.WriteTimeout != 0 {
		t.Errorf("expected default WriteTimeout=0 (unlimited), got %v", cfg.HTTP.WriteTimeout)
	}
	if cfg.HTTP.IdleTimeout != 120*time.Second {
		t.Errorf("expected default IdleTimeout=120s, got %v", cfg.HTTP.IdleTimeout)
	}
	if cfg.HTTP.MaxHeaderBytes != 1<<20 {
		t.Errorf("expected default MaxHeaderBytes=1MB, got %d", cfg.HTTP.MaxHeaderBytes)
	}
	if cfg.HTTP.MaxRequestBody != 10<<20 {
		t.Errorf("expected default MaxRequestBody=10MB, got %d", cfg.HTTP.MaxRequestBody)
	}
	if cfg.HTTP.CSP != "default-src 'none'; frame-ancestors 'none'" {
		t.Errorf("unexpected default CSP: %q", cfg.HTTP.CSP)
	}
	if cfg.HTTP.ReferrerPolicy != "no-referrer" {
		t.Errorf("unexpected default ReferrerPolicy: %q", cfg.HTTP.ReferrerPolicy)
	}
	if cfg.HTTP.PermissionsPolicy != "geolocation=(), camera=(), microphone=()" {
		t.Errorf("unexpected default PermissionsPolicy: %q", cfg.HTTP.PermissionsPolicy)
	}
	if !cfg.CORSStrict {
		t.Error("expected default CORSStrict=true (fail-closed)")
	}
}

// TestLoadCORSStrictOptOut verifies the documented escape hatch: setting
// CORS_STRICT=false restores the legacy permissive reflect-any-Origin behavior.
func TestLoadCORSStrictOptOut(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("CORS_STRICT", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CORSStrict {
		t.Error("expected CORSStrict=false when CORS_STRICT=false is set explicitly")
	}
}

func TestLoadHTTPHardeningCustom(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("HTTP_READ_HEADER_TIMEOUT", "2s")
	t.Setenv("HTTP_READ_TIMEOUT", "10s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "90s")
	t.Setenv("HTTP_IDLE_TIMEOUT", "60s")
	t.Setenv("HTTP_MAX_HEADER_BYTES", "2048")
	t.Setenv("MAX_REQUEST_BODY_BYTES", "4096")
	t.Setenv("HTTP_CSP", "default-src 'self'")
	t.Setenv("HTTP_REFERRER_POLICY", "same-origin")
	t.Setenv("HTTP_PERMISSIONS_POLICY", "camera=()")
	t.Setenv("CORS_STRICT", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTP.ReadHeaderTimeout != 2*time.Second {
		t.Errorf("expected ReadHeaderTimeout=2s, got %v", cfg.HTTP.ReadHeaderTimeout)
	}
	if cfg.HTTP.ReadTimeout != 10*time.Second {
		t.Errorf("expected ReadTimeout=10s, got %v", cfg.HTTP.ReadTimeout)
	}
	if cfg.HTTP.WriteTimeout != 90*time.Second {
		t.Errorf("expected WriteTimeout=90s, got %v", cfg.HTTP.WriteTimeout)
	}
	if cfg.HTTP.IdleTimeout != 60*time.Second {
		t.Errorf("expected IdleTimeout=60s, got %v", cfg.HTTP.IdleTimeout)
	}
	if cfg.HTTP.MaxHeaderBytes != 2048 {
		t.Errorf("expected MaxHeaderBytes=2048, got %d", cfg.HTTP.MaxHeaderBytes)
	}
	if cfg.HTTP.MaxRequestBody != 4096 {
		t.Errorf("expected MaxRequestBody=4096, got %d", cfg.HTTP.MaxRequestBody)
	}
	if cfg.HTTP.CSP != "default-src 'self'" {
		t.Errorf("expected CSP set, got %q", cfg.HTTP.CSP)
	}
	if cfg.HTTP.ReferrerPolicy != "same-origin" {
		t.Errorf("expected ReferrerPolicy=same-origin, got %q", cfg.HTTP.ReferrerPolicy)
	}
	if cfg.HTTP.PermissionsPolicy != "camera=()" {
		t.Errorf("expected PermissionsPolicy=camera=(), got %q", cfg.HTTP.PermissionsPolicy)
	}
	if !cfg.CORSStrict {
		t.Error("expected CORSStrict=true")
	}
}

func TestLoadHTTPHardeningInvalidFallback(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("HTTP_READ_HEADER_TIMEOUT", "not-a-duration")
	t.Setenv("HTTP_MAX_HEADER_BYTES", "not-an-int")
	t.Setenv("CORS_STRICT", "not-a-bool")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTP.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("expected fallback ReadHeaderTimeout=5s for invalid duration, got %v", cfg.HTTP.ReadHeaderTimeout)
	}
	if cfg.HTTP.MaxHeaderBytes != 1<<20 {
		t.Errorf("expected fallback MaxHeaderBytes=1MB for invalid int, got %d", cfg.HTTP.MaxHeaderBytes)
	}
	if !cfg.CORSStrict {
		t.Error("expected fallback CORSStrict=true for invalid bool")
	}
}

func TestLoadScopeDefaults(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OAuth.EnforceScopes {
		t.Error("expected default EnforceScopes=false")
	}
	if cfg.OAuth.RequiredScopes != nil {
		t.Errorf("expected default RequiredScopes=nil, got %v", cfg.OAuth.RequiredScopes)
	}
}

func TestLoadScopeCustom(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ENFORCE_SCOPES", "true")
	t.Setenv("REQUIRED_SCOPES", "research, tool:web_search")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.OAuth.EnforceScopes {
		t.Error("expected EnforceScopes=true")
	}
	if len(cfg.OAuth.RequiredScopes) != 2 || cfg.OAuth.RequiredScopes[0] != "research" || cfg.OAuth.RequiredScopes[1] != "tool:web_search" {
		t.Errorf("unexpected RequiredScopes: %v", cfg.OAuth.RequiredScopes)
	}
}

func TestLoadEnforceScopesInvalidFallback(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ENFORCE_SCOPES", "not-a-bool")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OAuth.EnforceScopes {
		t.Error("expected fallback EnforceScopes=false for invalid bool")
	}
}

func TestLoadRateLimitNewDefaults(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RateLimit.PerIP != 0 {
		t.Errorf("expected default PerIP=0, got %d", cfg.RateLimit.PerIP)
	}
	if cfg.RateLimit.TrustProxy {
		t.Error("expected default TrustProxy=false")
	}
	if cfg.RateLimit.Persist {
		t.Error("expected default Persist=false")
	}
}

func TestLoadRateLimitNewCustom(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("RATE_LIMIT_PER_IP", "300")
	t.Setenv("TRUST_PROXY", "true")
	t.Setenv("RATE_LIMIT_PERSIST", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RateLimit.PerIP != 300 {
		t.Errorf("expected PerIP=300, got %d", cfg.RateLimit.PerIP)
	}
	if !cfg.RateLimit.TrustProxy {
		t.Error("expected TrustProxy=true")
	}
	if !cfg.RateLimit.Persist {
		t.Error("expected Persist=true")
	}
}

func TestLoadRateLimitPerIPInvalidFallback(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("RATE_LIMIT_PER_IP", "not-an-int")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RateLimit.PerIP != 0 {
		t.Errorf("expected fallback PerIP=0, got %d", cfg.RateLimit.PerIP)
	}
}

func TestLoadAuditNewDefaults(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Audit.MaxBytes != 100<<20 {
		t.Errorf("expected default Audit.MaxBytes=100MB, got %d", cfg.Audit.MaxBytes)
	}
	if cfg.Audit.RetentionDays != 180 {
		t.Errorf("expected default Audit.RetentionDays=180, got %d", cfg.Audit.RetentionDays)
	}
}

func TestLoadAuditMaxBytesCustom(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("AUDIT_MAX_BYTES", "5242880")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Audit.MaxBytes != 5242880 {
		t.Errorf("expected Audit.MaxBytes=5242880, got %d", cfg.Audit.MaxBytes)
	}
}

func TestLoadAuditMaxBytesInvalidFallback(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("AUDIT_MAX_BYTES", "not-an-int")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Audit.MaxBytes != 100<<20 {
		t.Errorf("expected fallback Audit.MaxBytes=100MB, got %d", cfg.Audit.MaxBytes)
	}
}

func TestLoadAuditRetentionClamp(t *testing.T) {
	tests := []struct {
		env      string
		expected int
	}{
		{"0", 0},        // disabled, not clamped
		{"1", 180},      // below floor clamps up
		{"179", 180},    // just below floor
		{"180", 180},    // at floor
		{"365", 365},    // within range, unchanged
		{"3650", 3650},  // at ceiling
		{"99999", 3650}, // above ceiling clamps down
	}
	for _, tt := range tests {
		t.Run("AUDIT_RETENTION_DAYS="+tt.env, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("AUDIT_RETENTION_DAYS", tt.env)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Audit.RetentionDays != tt.expected {
				t.Errorf("for AUDIT_RETENTION_DAYS=%s expected %d, got %d", tt.env, tt.expected, cfg.Audit.RetentionDays)
			}
		})
	}
}

func TestLoadAuditRetentionInvalidFallback(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("AUDIT_RETENTION_DAYS", "not-an-int")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// envInt falls back to 180 (the default), which is the clamp floor.
	if cfg.Audit.RetentionDays != 180 {
		t.Errorf("expected fallback Audit.RetentionDays=180, got %d", cfg.Audit.RetentionDays)
	}
}

func TestLoadAdminAPIKeyTooShort(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ADMIN_API_KEY", strings.Repeat("a", 15)) // 15 chars

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for ADMIN_API_KEY shorter than 16 chars")
	}
	if !strings.Contains(err.Error(), "ADMIN_API_KEY must be at least 16 characters") {
		t.Errorf("expected error about admin key length, got: %v", err)
	}
}

func TestLoadAdminAPIKeyAccepted(t *testing.T) {
	setRequiredEnv(t)
	key := strings.Repeat("a", 16) // 16 chars
	t.Setenv("ADMIN_API_KEY", key)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error with 16-char admin key: %v", err)
	}
	if cfg.AdminAPIKey != key {
		t.Errorf("expected AdminAPIKey to be set")
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("expected no warnings when only ADMIN_API_KEY is set, got: %v", cfg.Warnings)
	}
}

func TestLoadAdminAPIKeyEmptyAllowed(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ADMIN_API_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error with empty admin key: %v", err)
	}
	if cfg.AdminAPIKey != "" {
		t.Errorf("expected empty AdminAPIKey")
	}
}

// TestLoadLegacyCacheAdminKeyAccepted verifies backward compatibility: the
// deprecated CACHE_ADMIN_KEY still works and produces a deprecation warning.
func TestLoadLegacyCacheAdminKeyAccepted(t *testing.T) {
	setRequiredEnv(t)
	key := strings.Repeat("b", 20)
	t.Setenv("CACHE_ADMIN_KEY", key)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error with legacy admin key: %v", err)
	}
	if cfg.AdminAPIKey != key {
		t.Errorf("expected legacy CACHE_ADMIN_KEY to populate AdminAPIKey")
	}
	if len(cfg.Warnings) == 0 {
		t.Error("expected a deprecation warning for CACHE_ADMIN_KEY")
	}
}

// TestLoadAdminKeyPrecedence verifies ADMIN_API_KEY wins when both are set and
// a warning is emitted to remove the deprecated name.
func TestLoadAdminKeyPrecedence(t *testing.T) {
	setRequiredEnv(t)
	newKey := strings.Repeat("c", 18)
	t.Setenv("ADMIN_API_KEY", newKey)
	t.Setenv("CACHE_ADMIN_KEY", strings.Repeat("d", 18))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AdminAPIKey != newKey {
		t.Errorf("expected ADMIN_API_KEY to take precedence over CACHE_ADMIN_KEY")
	}
	if len(cfg.Warnings) == 0 {
		t.Error("expected a warning when both admin key vars are set")
	}
}

// TestLoadLegacyCacheAdminKeyTooShort verifies the length error message names
// the legacy variable when only the legacy variable is set.
func TestLoadLegacyCacheAdminKeyTooShort(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("CACHE_ADMIN_KEY", strings.Repeat("a", 15))

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for CACHE_ADMIN_KEY shorter than 16 chars")
	}
	if !strings.Contains(err.Error(), "CACHE_ADMIN_KEY must be at least 16 characters") {
		t.Errorf("expected error to name the legacy var, got: %v", err)
	}
}

func TestLoadCacheEncryptionKeyPrevValidation(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("CACHE_ENCRYPTION_KEY_PREV", "not-hex-and-too-short")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid CACHE_ENCRYPTION_KEY_PREV")
	}
	if !strings.Contains(err.Error(), "CACHE_ENCRYPTION_KEY_PREV must be exactly 64 hex characters") {
		t.Errorf("expected error about prev key, got: %v", err)
	}
}

func TestLoadCacheEncryptionKeyPrevNonHex64(t *testing.T) {
	setRequiredEnv(t)
	// Exactly 64 chars but not all hex (contains 'g').
	t.Setenv("CACHE_ENCRYPTION_KEY_PREV", strings.Repeat("g", 64))

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for 64-char non-hex CACHE_ENCRYPTION_KEY_PREV")
	}
	if !strings.Contains(err.Error(), "CACHE_ENCRYPTION_KEY_PREV must be exactly 64 hex characters") {
		t.Errorf("expected error about prev key, got: %v", err)
	}
}

func TestLoadCacheEncryptionKeyPrevValid(t *testing.T) {
	setRequiredEnv(t)
	validKey := strings.Repeat("ab", 32) // 64 hex chars
	t.Setenv("CACHE_ENCRYPTION_KEY_PREV", validKey)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error with valid prev key: %v", err)
	}
	if cfg.CacheEncryptionKeyPrev != validKey {
		t.Errorf("expected CacheEncryptionKeyPrev to be set")
	}
}

func TestLoadDataRegion(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DataRegion != "" {
		t.Errorf("expected default DataRegion empty, got %q", cfg.DataRegion)
	}

	t.Setenv("DATA_REGION", "eu-central-1")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DataRegion != "eu-central-1" {
		t.Errorf("expected DataRegion=eu-central-1, got %q", cfg.DataRegion)
	}
}

func TestIsHex64(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{strings.Repeat("a", 64), true},
		{strings.Repeat("A", 64), true},
		{strings.Repeat("0", 64), true},
		{strings.Repeat("F", 64), true},
		{strings.Repeat("a", 63), false}, // too short
		{strings.Repeat("a", 65), false}, // too long
		{strings.Repeat("g", 64), false}, // non-hex
		{"", false},
	}
	for _, tt := range tests {
		if got := isHex64(tt.in); got != tt.want {
			t.Errorf("isHex64(len=%d) = %v, want %v", len(tt.in), got, tt.want)
		}
	}
}

func TestConfigStillReturnedOnError(t *testing.T) {
	t.Setenv("SEARCH_PROVIDER", "google")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "")

	cfg, err := Load()
	if err == nil {
		t.Fatal("expected validation error")
	}
	// Config should still be returned (partial) even with errors
	if cfg == nil {
		t.Fatal("expected non-nil config even on error")
	}
}
